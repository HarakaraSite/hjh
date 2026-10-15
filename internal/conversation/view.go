// Package conversation owns a materialized, coalescing view of saved execution
// facts. Persistence and public run state remain owned by their own packages.
package conversation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"hjh/internal/api"
	"hjh/internal/contracts"
)

var ErrClosed = errors.New("conversation view is closed")

type View struct {
	scopeID      string
	viewID       string
	sessionScope bool

	mu                sync.Mutex
	entities          map[string]api.ConversationEntity
	order             []string
	callEntities      map[string]string
	executionEntities map[string]string
	revision          uint64
	through           *uint64
	rootCuts          []api.ConversationCut
	snapshotCache     *api.ConversationSnapshot
	updateCache       *sharedUpdate

	loaded     bool
	loadErr    error
	ready      chan struct{}
	readyOnce  sync.Once
	startDrain chan struct{}
	startOnce  sync.Once

	// The execution-data owner calls Enqueue from its write gate. Keep this
	// lock independent from entity normalization, ordering, and snapshot copies.
	inboxMu            sync.Mutex
	pending            map[string]contracts.ExecutionFact
	pendingThrough     *uint64
	pendingRootThrough map[string]uint64
	pendingRootOrdinal map[string]int64
	wake               chan struct{}
	progress           chan struct{}
	remove             chan *subscriber
	done               chan struct{}
	closeOnce          sync.Once

	subscribers map[*subscriber]struct{}
}

type subscriber struct {
	notify       chan struct{}
	pending      map[string]api.ConversationEntity
	baseRevision uint64
	revision     uint64
	through      *uint64
	scopeID      string
	viewID       string
}

type sharedUpdate struct {
	key     string
	update  api.ConversationUpdate
	waiting map[*subscriber]struct{}
}

// Subscription owns only a notification handle and coalesced updates. It does
// not retain the run manager or its execution data owner.
type Subscription struct {
	Notify   <-chan struct{}
	viewDone <-chan struct{}
	remove   chan<- *subscriber
	state    *subscriber
	owner    *View
	mu       *sync.Mutex
	once     sync.Once
}

func NewView(scopeID string) *View {
	return newView(scopeID, false)
}

func NewSessionView(scopeID string) *View {
	return newView(scopeID, true)
}

func newView(scopeID string, sessionScope bool) *View {
	v := &View{
		scopeID: scopeID, viewID: uuid.NewString(), sessionScope: sessionScope, entities: map[string]api.ConversationEntity{},
		callEntities: map[string]string{}, executionEntities: map[string]string{},
		ready: make(chan struct{}), startDrain: make(chan struct{}), pending: map[string]contracts.ExecutionFact{}, pendingRootThrough: map[string]uint64{}, pendingRootOrdinal: map[string]int64{},
		wake: make(chan struct{}, 1), progress: make(chan struct{}),
		remove: make(chan *subscriber, 32), done: make(chan struct{}), subscribers: map[*subscriber]struct{}{},
	}
	go v.drain()
	return v
}

// LoadCut restores one fixed history cut. Live commits may be enqueued while
// the walker runs; the drain applies them only after the cut is installed.
func (v *View) LoadCut(ctx context.Context, through *uint64, rootCuts []api.ConversationCut, walk func(func(contracts.ExecutionFact) error) error) error {
	var err error
	if walk != nil {
		err = walk(func(fact contracts.ExecutionFact) error {
			if ctx != nil {
				if canceled := ctx.Err(); canceled != nil {
					return canceled
				}
			}
			v.mu.Lock()
			v.applyFactLocked(fact)
			v.mu.Unlock()
			return nil
		})
	}
	v.mu.Lock()
	if err == nil {
		v.through = cloneSequence(through)
		v.rootCuts = append([]api.ConversationCut(nil), rootCuts...)
	} else {
		v.loadErr = err
	}
	v.loaded = true
	v.mu.Unlock()
	v.readyOnce.Do(func() { close(v.ready) })
	return err
}

// Enqueue is called from the execution data owner's commit gate. It only
// replaces immutable latest facts, advances the saved prefix, and wakes drain.
func (v *View) Enqueue(commit contracts.ExecutionCommit) {
	if commit.SequenceCount == 0 && len(commit.Facts) == 0 {
		return
	}
	v.inboxMu.Lock()
	if isClosed(v.done) {
		v.inboxMu.Unlock()
		return
	}
	for _, fact := range commit.Facts {
		key, ok := entityKey(fact)
		if !ok {
			continue
		}
		fact.Thinking = append([]contracts.ThinkingItem(nil), fact.Thinking...)
		fact.Diagnostic = contracts.CloneFailure(fact.Diagnostic)
		if previous, exists := v.pending[key]; exists && previous.Conversation.FirstSequence != 0 {
			// A terminal/update source may carry the latest fact sequence in its
			// metadata. Keep the first committed position when several versions
			// are coalesced before the materializer sees the entity.
			fact.Conversation.FirstSequence = previous.Conversation.FirstSequence
		}
		v.pending[key] = fact
	}
	rootID := commit.RootExecutionID
	rootOrdinal := int64(0)
	if rootID == "" && len(commit.Facts) > 0 {
		rootID = commit.Facts[0].RootExecutionID
	}
	for _, fact := range commit.Facts {
		if fact.RootExecutionID == rootID && fact.RootExecutionOrdinal != 0 {
			rootOrdinal = fact.RootExecutionOrdinal
			break
		}
	}
	if rootID != "" && commit.ThroughSequence != 0 {
		v.pendingRootThrough[rootID] = commit.ThroughSequence
		if rootOrdinal != 0 {
			v.pendingRootOrdinal[rootID] = rootOrdinal
		}
	}
	if !v.sessionScope && commit.ThroughSequence != 0 {
		v.pendingThrough = sequencePointer(commit.ThroughSequence)
	}
	v.inboxMu.Unlock()
	v.signal()
}

func (v *View) signal() {
	select {
	case v.wake <- struct{}{}:
	default:
	}
}

// SubscribeSnapshot atomically captures the current view and registers a
// consumer for all later upserts.
// SubscribeSnapshot returns the immutable cached value for the captured view
// revision. Consumers share its entity map and must not mutate the result.
func (v *View) SubscribeSnapshot(ctx context.Context) (api.ConversationSnapshot, *Subscription, error) {
	select {
	case <-ctx.Done():
		return api.ConversationSnapshot{}, nil, ctx.Err()
	case <-v.ready:
	}
	v.mu.Lock()
	if v.loadErr != nil {
		v.mu.Unlock()
		return api.ConversationSnapshot{}, nil, v.loadErr
	}
	if isClosed(v.done) {
		v.mu.Unlock()
		return api.ConversationSnapshot{}, nil, ErrClosed
	}
	state := &subscriber{notify: make(chan struct{}, 1), pending: map[string]api.ConversationEntity{}, baseRevision: v.revision, revision: v.revision, through: cloneSequence(v.through), scopeID: v.scopeID, viewID: v.viewID}
	v.subscribers[state] = struct{}{}
	snapshot := v.snapshotLocked()
	v.mu.Unlock()
	// Publish the initial fixed-cut snapshot and register its first consumer
	// before the post-cut inbox is allowed to reach the materializer.
	v.startOnce.Do(func() { close(v.startDrain) })
	v.signal()
	return snapshot, &Subscription{Notify: state.notify, viewDone: v.done, remove: v.remove, state: state, owner: v, mu: &v.mu}, nil
}

// Snapshot returns the immutable cached value for the current view revision.
// Consumers share its entity map and must not mutate the result.
func (v *View) Snapshot(ctx context.Context) (api.ConversationSnapshot, error) {
	select {
	case <-ctx.Done():
		return api.ConversationSnapshot{}, ctx.Err()
	case <-v.ready:
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.loadErr != nil {
		return api.ConversationSnapshot{}, v.loadErr
	}
	if isClosed(v.done) {
		return api.ConversationSnapshot{}, ErrClosed
	}
	return v.snapshotLocked(), nil
}

// WaitThrough waits until the independently running materializer has applied
// the fixed durable prefix required for a terminal SSE snapshot.
func (v *View) WaitThrough(ctx context.Context, target uint64) error {
	return v.waitThrough(ctx, target, nil)
}

func (v *View) waitThrough(ctx context.Context, target uint64, waiting func()) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-v.done:
			return ErrClosed
		case <-v.ready:
		}
		v.mu.Lock()
		through := v.through
		loadErr := v.loadErr
		loaded := v.loaded
		progress := v.progress
		v.mu.Unlock()
		if loadErr != nil {
			return loadErr
		}
		if loaded && (target == 0 || through != nil && *through >= target) {
			return nil
		}
		if waiting != nil {
			waiting()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-v.done:
			return ErrClosed
		case <-progress:
		}
	}
}

func (v *View) snapshotLocked() api.ConversationSnapshot {
	if v.snapshotCache != nil && v.snapshotCache.Revision == v.revision {
		return *v.snapshotCache
	}
	entities := make(map[string]api.ConversationEntity, len(v.entities))
	for id, entity := range v.entities {
		entities[id] = cloneEntity(entity)
	}
	snapshot := api.ShareConversationSnapshot(api.ConversationSnapshot{ScopeID: v.scopeID, ViewID: v.viewID, Revision: v.revision,
		ThroughSequence: cloneSequence(v.through), RootCuts: append([]api.ConversationCut(nil), v.rootCuts...),
		Order: append([]string(nil), v.order...), Entities: entities})
	v.snapshotCache = &snapshot
	return snapshot
}

// TakeUpdate swaps one consumer's latest changed entities and prefix. Multiple
// materializer revisions can be merged into one update without retaining body
// history. Consumers with the same change set share its immutable upserts and
// encoded JSON until each has taken the update.
func (s *Subscription) TakeUpdate() (api.ConversationUpdate, bool) {
	state := s.state
	if state == nil || s.mu == nil {
		return api.ConversationUpdate{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if state.baseRevision == state.revision {
		return api.ConversationUpdate{}, false
	}
	ids := make([]string, 0, len(state.pending))
	for id := range state.pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	key := updateKey(state.baseRevision, state.revision, ids)
	var update api.ConversationUpdate
	cache := s.owner.updateCache
	if cache != nil && cache.key == key {
		update = cache.update
	} else {
		upserts := make([]api.ConversationEntity, 0, len(ids))
		for _, id := range ids {
			entity, exists := s.owner.entities[id]
			if !exists {
				entity = state.pending[id]
			}
			upserts = append(upserts, cloneEntity(entity))
		}
		sort.Slice(upserts, func(i, j int) bool { return less(upserts[i], upserts[j]) })
		update = api.ShareConversationUpdate(api.ConversationUpdate{ScopeID: state.scopeID, ViewID: state.viewID, BaseRevision: state.baseRevision,
			Revision: state.revision, ThroughSequence: cloneSequence(state.through), Upserts: upserts})
		waiting := make(map[*subscriber]struct{})
		for candidate := range s.owner.subscribers {
			if candidate.baseRevision == state.baseRevision && candidate.revision == state.revision {
				waiting[candidate] = struct{}{}
			}
		}
		if len(waiting) == 0 {
			waiting[state] = struct{}{}
		}
		cache = &sharedUpdate{key: key, update: update, waiting: waiting}
		s.owner.updateCache = cache
	}
	delete(cache.waiting, state)
	if len(cache.waiting) == 0 && s.owner.updateCache == cache {
		s.owner.updateCache = nil
	}
	state.pending = map[string]api.ConversationEntity{}
	state.baseRevision = state.revision
	return update, true
}

func updateKey(baseRevision, revision uint64, ids []string) string {
	key := make([]byte, 0, 32+len(ids)*70)
	key = strconv.AppendUint(key, baseRevision, 10)
	key = append(key, '/')
	key = strconv.AppendUint(key, revision, 10)
	for _, id := range ids {
		key = append(key, 0)
		key = append(key, id...)
	}
	return string(key)
}

func (s *Subscription) Close() {
	s.once.Do(func() {
		if s.state == nil {
			return
		}
		select {
		case s.remove <- s.state:
		case <-s.viewDone:
		}
	})
}

func (v *View) Release() {
	v.closeOnce.Do(func() {
		v.mu.Lock()
		close(v.done)
		v.entities = nil
		v.order = nil
		v.callEntities = nil
		v.executionEntities = nil
		v.subscribers = nil
		v.snapshotCache = nil
		v.updateCache = nil
		close(v.progress)
		v.mu.Unlock()
		v.inboxMu.Lock()
		v.pending = nil
		v.pendingThrough = nil
		v.pendingRootThrough = nil
		v.pendingRootOrdinal = nil
		v.inboxMu.Unlock()
	})
}

func (v *View) drain() {
	for {
		select {
		case <-v.done:
			return
		case state := <-v.remove:
			v.mu.Lock()
			delete(v.subscribers, state)
			if v.updateCache != nil {
				delete(v.updateCache.waiting, state)
				if len(v.updateCache.waiting) == 0 {
					v.updateCache = nil
				}
			}
			if len(v.subscribers) == 0 {
				v.updateCache = nil
				v.snapshotCache = nil
			}
			v.mu.Unlock()
		case <-v.wake:
			<-v.ready
			select {
			case <-v.done:
				return
			case <-v.startDrain:
			}
			v.drainPending()
		}
	}
}

func (v *View) drainPending() {
	v.inboxMu.Lock()
	facts := v.pending
	v.pending = map[string]contracts.ExecutionFact{}
	through := v.pendingThrough
	v.pendingThrough = nil
	rootThrough := v.pendingRootThrough
	v.pendingRootThrough = map[string]uint64{}
	rootOrdinals := v.pendingRootOrdinal
	v.pendingRootOrdinal = map[string]int64{}
	v.inboxMu.Unlock()
	if len(facts) == 0 && through == nil && len(rootThrough) == 0 {
		return
	}

	ordered := make([]contracts.ExecutionFact, 0, len(facts))
	for _, fact := range facts {
		ordered = append(ordered, fact)
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i].Conversation, ordered[j].Conversation
		if a.Sequence != b.Sequence {
			return a.Sequence < b.Sequence
		}
		if a.ItemIndex != b.ItemIndex {
			return a.ItemIndex < b.ItemIndex
		}
		if a.FirstSequence != b.FirstSequence {
			return a.FirstSequence < b.FirstSequence
		}
		return ordered[i].ExecutionID < ordered[j].ExecutionID
	})
	v.mu.Lock()
	if isClosed(v.done) {
		v.mu.Unlock()
		return
	}
	changed := false
	upserts := make([]api.ConversationEntity, 0, len(ordered))
	for _, fact := range ordered {
		if entity, ok := v.applyFactLocked(fact); ok {
			upserts = append(upserts, entity)
			changed = true
		}
	}
	for rootID, sequence := range rootThrough {
		index := -1
		for i := range v.rootCuts {
			if v.rootCuts[i].RootExecutionID == rootID {
				index = i
				break
			}
		}
		if index < 0 {
			v.rootCuts = append(v.rootCuts, api.ConversationCut{RootExecutionID: rootID,
				RootExecutionOrdinal: rootOrdinals[rootID], ThroughSequence: sequencePointer(sequence)})
		} else {
			v.rootCuts[index].ThroughSequence = sequencePointer(sequence)
			if rootOrdinals[rootID] != 0 {
				v.rootCuts[index].RootExecutionOrdinal = rootOrdinals[rootID]
			}
		}
		changed = true
	}
	if through != nil && (v.through == nil || *through > *v.through) {
		v.through = cloneSequence(through)
		changed = true
	}
	if changed {
		v.revision++
		v.snapshotCache = nil
		v.updateCache = nil
		progress := v.progress
		v.progress = make(chan struct{})
		close(progress)
		for state := range v.subscribers {
			for _, entity := range upserts {
				state.pending[entity.ID] = cloneEntity(entity)
			}
			state.revision = v.revision
			state.through = cloneSequence(v.through)
			select {
			case state.notify <- struct{}{}:
			default:
			}
		}
	}
	v.mu.Unlock()
}

func (v *View) applyFactLocked(fact contracts.ExecutionFact) (api.ConversationEntity, bool) {
	_, ok := entityKey(fact)
	if !ok {
		return api.ConversationEntity{}, false
	}
	id := stableID(fact.ExecutionID, fact.Conversation.EntityKind, fact.Conversation.LocalKey)
	entity := api.ConversationEntity{ID: id, Kind: fact.Conversation.EntityKind, ExecutionID: fact.ExecutionID,
		ParentExecutionID: fact.ParentExecutionID, Position: api.ConversationPosition{RootExecutionOrdinal: fact.RootExecutionOrdinal,
			FirstSequence: fact.Conversation.FirstSequence, ItemIndex: fact.Conversation.ItemIndex},
		Text: fact.Text, Preview: fact.Preview, Status: entityStatus(fact), Name: fact.Name, Outcome: fact.Outcome,
		Error: fact.Error, ThinkingKind: fact.ThinkingKind, Diagnostic: contracts.CloneFailure(fact.Diagnostic)}
	if entity.Kind == "tool" {
		entity.Text = ""
	}
	if fact.Locator.RecordID != "" {
		entity.Detail = &api.ConversationDetailLocator{ExecutionID: fact.Locator.ExecutionID,
			RecordID: fact.Locator.RecordID, Ordinal: fact.Locator.Ordinal, CallID: fact.CallID}
	}
	if fact.ParentCallID != "" {
		// Nested calls normally refer to the parent call in the same execution.
		// A child execution's task/start fact instead points to its parent's
		// spawn call, so use the parent execution only when the local key misses.
		entity.ParentEntityID = v.callEntities[fact.ExecutionID+"/"+fact.ParentCallID]
		if entity.ParentEntityID == "" && fact.ParentExecutionID != "" {
			entity.ParentEntityID = v.callEntities[fact.ParentExecutionID+"/"+fact.ParentCallID]
		}
	}
	if entity.ParentEntityID == "" && fact.ParentExecutionID != "" {
		entity.ParentEntityID = v.executionEntities[fact.ParentExecutionID]
	}
	if previous, exists := v.entities[id]; exists {
		if entity.Text == "" {
			entity.Text = previous.Text
		}
		if entity.Name == "" {
			entity.Name = previous.Name
		}
		if entity.Preview == "" {
			entity.Preview = previous.Preview
		}
		if entity.ParentEntityID == "" {
			entity.ParentEntityID = previous.ParentEntityID
		}
		if previous.Detail != nil && entity.Detail == nil {
			entity.Detail = previous.Detail
		}
		if previous.Position.FirstSequence != entity.Position.FirstSequence {
			entity.Position.FirstSequence = previous.Position.FirstSequence
		}
	} else {
		v.insertOrderLocked(entity)
	}
	v.entities[id] = entity
	if fact.CallID != "" {
		v.callEntities[fact.ExecutionID+"/"+fact.CallID] = id
	}
	if fact.Conversation.EntityKind == "execution" {
		v.executionEntities[fact.ExecutionID] = id
	}
	return entity, true
}

func (v *View) insertOrderLocked(entity api.ConversationEntity) {
	index := sort.Search(len(v.order), func(i int) bool { return !less(v.entities[v.order[i]], entity) })
	v.order = append(v.order, "")
	copy(v.order[index+1:], v.order[index:])
	v.order[index] = entity.ID
}

func less(a, b api.ConversationEntity) bool {
	if a.Position.RootExecutionOrdinal != b.Position.RootExecutionOrdinal {
		return a.Position.RootExecutionOrdinal < b.Position.RootExecutionOrdinal
	}
	if a.Position.FirstSequence != b.Position.FirstSequence {
		return a.Position.FirstSequence < b.Position.FirstSequence
	}
	if a.Position.ItemIndex != b.Position.ItemIndex {
		return a.Position.ItemIndex < b.Position.ItemIndex
	}
	return a.ID < b.ID
}

func entityKey(fact contracts.ExecutionFact) (string, bool) {
	if fact.ExecutionID == "" || fact.Conversation.EntityKind == "" || fact.Conversation.LocalKey == "" {
		return "", false
	}
	return fact.ExecutionID + "\x00" + fact.Conversation.EntityKind + "\x00" + fact.Conversation.LocalKey, true
}

func stableID(executionID, kind, localKey string) string {
	canonical, _ := json.Marshal([]string{executionID, kind, localKey})
	digest := sha256.Sum256(canonical)
	return "cv:" + hex.EncodeToString(digest[:])
}

func entityStatus(fact contracts.ExecutionFact) string {
	if fact.Phase == "superseded" {
		return "superseded"
	}
	if fact.Conversation.EntityKind == "task" {
		return "completed"
	}
	phase := strings.ToLower(fact.Phase)
	if fact.Error != "" || fact.Outcome == "cancelled" || fact.Outcome == "failed" || fact.Outcome == "interrupted" || phase == "incomplete" {
		return "incomplete"
	}
	switch phase {
	case "started", "start", "delta", "streaming", "running":
		return "streaming"
	case "completed", "complete", "result", "end", "terminal", "succeeded", "finished":
		return "completed"
	}
	if fact.Outcome == "completed" || fact.Outcome == "success" || fact.Outcome == "succeeded" {
		return "completed"
	}
	return "streaming"
}

func cloneSequence(sequence *uint64) *uint64 {
	if sequence == nil {
		return nil
	}
	copy := *sequence
	return &copy
}

func sequencePointer(sequence uint64) *uint64 { return &sequence }

func cloneEntity(entity api.ConversationEntity) api.ConversationEntity {
	entity.Diagnostic = contracts.CloneFailure(entity.Diagnostic)
	if entity.Detail != nil {
		detail := *entity.Detail
		entity.Detail = &detail
	}
	return entity
}

func isClosed(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}
