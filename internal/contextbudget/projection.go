package contextbudget

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"hjh/internal/contracts"
)

const (
	HistoryNone    = "none"
	HistoryFull    = "full"
	HistoryPartial = "partial"
	HistoryOmitted = "omitted"
	Estimator      = "wire-utf8-bytes/3+framing-estimate"
)

type Source struct {
	Kind           string `json:"kind"`
	ExecutionID    string `json:"executionId"`
	Turn           int64  `json:"turn"`
	MessageOrdinal int64  `json:"messageOrdinal"`
}

type Entry struct {
	Message contracts.Message `json:"message"`
	Source  Source            `json:"source"`
}

type Turn struct {
	Number  int64   `json:"number"`
	Entries []Entry `json:"entries"`
}

// HistoryReader returns the most recent turn when beforeTurn <= 0 and then
// strictly older turns as beforeTurn is advanced. A nil turn ends iteration.
type HistoryReader func(ctx context.Context, beforeTurn int64) (*Turn, error)

type HistoryTurnFact struct {
	Number          int64    `json:"number"`
	Status          string   `json:"status"` // full, partial, or omitted
	SelectedSources []Source `json:"selectedSources,omitempty"`
	OmittedSources  []Source `json:"omittedSources,omitempty"`
	Reason          string   `json:"reason,omitempty"`
}

type HistoryFact struct {
	Status string            `json:"status"` // none, full, partial, or omitted
	Reason string            `json:"reason,omitempty"`
	Turns  []HistoryTurnFact `json:"turns,omitempty"`
}

type Fact struct {
	Budget                 Budget      `json:"budget"`
	Estimator              string      `json:"estimator"`
	WireBytes              int64       `json:"wireBytes"`
	EstimatedTokens        int64       `json:"estimatedTokens"`
	HistoryWireBytes       *int64      `json:"historyWireBytes,omitempty"`
	HistoryEstimatedTokens *int64      `json:"historyEstimatedTokens,omitempty"`
	Sources                []Source    `json:"sources"`
	OmittedCurrent         []int       `json:"omittedCurrent"`
	OmittedCurrentSources  []Source    `json:"omittedCurrentSources,omitempty"`
	HistoryStatus          string      `json:"historyStatus"`
	History                HistoryFact `json:"history"`
}

type Projection struct {
	Request contracts.ModelRequest `json:"request"`
	Fact    Fact                   `json:"fact"`
	Sources []Source               `json:"sources"`
}

type ExceededError struct {
	Projection Projection
}

func (e *ExceededError) Error() string {
	if e == nil {
		return "context budget exceeded"
	}
	limit := "unknown"
	if e.Projection.Fact.Budget.Limit != nil {
		limit = fmt.Sprintf("%d", *e.Projection.Fact.Budget.Limit)
	}
	return fmt.Sprintf("estimated input %d tokens exceeds limit %s",
		e.Projection.Fact.EstimatedTokens, limit)
}

func (*ExceededError) Outcome() string { return "context_budget_exceeded" }

type Projector struct {
	budget  Budget
	history HistoryReader
	prefix  []Entry

	omittedCurrentIndices  map[int]struct{}
	currentTrimmingStarted bool
}

func New(budget Budget, history HistoryReader, prefix []Entry) *Projector {
	return &Projector{
		budget:                cloneBudget(budget),
		history:               history,
		prefix:                append([]Entry(nil), prefix...),
		omittedCurrentIndices: make(map[int]struct{}),
	}
}

// Project builds a request-only message projection and measures each candidate
// using the selected adapter's prepare function. request.Messages is expected
// to be empty; the returned request contains prefix, selected history, then
// current entries in source order.
func (p *Projector) Project(
	ctx context.Context,
	request contracts.ModelRequest,
	current []Entry,
	prepare func(contracts.ModelRequest) ([]byte, error),
) (Projection, error) {
	if prepare == nil {
		return Projection{}, errors.New("context budget projection requires an adapter prepare function")
	}

	currentUnits := projectionUnits(current)
	protectedCurrent := protectedCurrentIndices(current, currentUnits)
	currentIndices := p.currentIndices(len(current))
	evaluation, err := p.evaluate(request, current, currentIndices, nil, prepare)
	if err != nil {
		return Projection{}, err
	}
	if !evaluation.fits(p.budget) {
		for _, unit := range oldestRemovableUnits(currentUnits, protectedCurrent) {
			if unit.touchesAny(p.omittedCurrentIndices) {
				continue
			}
			p.currentTrimmingStarted = true
			for _, index := range unit.indices {
				p.omittedCurrentIndices[index] = struct{}{}
			}
			currentIndices = p.currentIndices(len(current))
			evaluation, err = p.evaluate(request, current, currentIndices, nil, prepare)
			if err != nil {
				return Projection{}, err
			}
			if evaluation.fits(p.budget) {
				break
			}
		}
	}

	if !evaluation.fits(p.budget) {
		projection := p.makeProjection(evaluation, nil, currentIndices, current, HistoryFact{
			Status: HistoryOmitted,
			Reason: "protected_current_does_not_fit",
		})
		return projection, contracts.WithFailure(&ExceededError{Projection: projection},
			contracts.FailureDiagnostic{Code: contracts.FailureContextBudget})
	}

	if p.currentTrimmingStarted {
		projection, err := p.finishWithoutHistory(request, current, currentIndices, prepare,
			HistoryFact{Status: HistoryOmitted, Reason: "current_trim_started"})
		return projection, err
	}
	if p.history == nil {
		projection, err := p.finishWithoutHistory(request, current, currentIndices, prepare,
			HistoryFact{Status: HistoryNone})
		return projection, err
	}

	return p.projectHistory(ctx, request, current, currentIndices, prepare)
}

type historySelection struct {
	turn        *Turn
	units       []projectionUnit
	keptIndices []int
	omitted     map[int]struct{}
	status      string
	reason      string
}

type evaluation struct {
	request                contracts.ModelRequest
	wireBytes              int64
	estimatedTokens        int64
	historyWireBytes       *int64
	historyEstimatedTokens *int64
}

func (e evaluation) fits(budget Budget) bool {
	if budget.Limit != nil && e.estimatedTokens > *budget.Limit {
		return false
	}
	if budget.HistoryTokens != nil && e.historyEstimatedTokens != nil &&
		*e.historyEstimatedTokens > *budget.HistoryTokens {
		return false
	}
	return true
}

func (p *Projector) evaluate(
	request contracts.ModelRequest,
	current []Entry,
	currentIndices []int,
	history []*historySelection,
	prepare func(contracts.ModelRequest) ([]byte, error),
) (evaluation, error) {
	messages := make([]contracts.Message, 0, len(p.prefix)+len(currentIndices))
	for _, entry := range p.prefix {
		messages = append(messages, entry.Message)
	}
	for _, selection := range chronologicalHistory(history) {
		for _, index := range selection.keptIndices {
			entry := selection.turn.Entries[index]
			messages = append(messages, entry.Message)
		}
	}
	for _, index := range currentIndices {
		if index >= 0 && index < len(current) {
			entry := current[index]
			messages = append(messages, entry.Message)
		}
	}

	projectedRequest := request
	projectedRequest.Messages = messages
	projectedRequest.WireBody = nil
	wire, err := prepare(projectedRequest)
	if err != nil {
		return evaluation{}, err
	}
	result := evaluation{
		request:         projectedRequest,
		wireBytes:       int64(len(wire)),
		estimatedTokens: estimateTokens(len(wire), len(messages)),
	}

	if p.budget.HistoryTokens != nil {
		historyEntries := chronologicalHistory(history)
		historyMessages := make([]contracts.Message, 0)
		for _, selection := range historyEntries {
			for _, index := range selection.keptIndices {
				historyMessages = append(historyMessages, selection.turn.Entries[index].Message)
			}
		}
		if len(historyMessages) != 0 {
			historyRequest := contracts.ModelRequest{
				Model:    request.Model,
				Messages: historyMessages,
			}
			historyWire, prepareErr := prepare(historyRequest)
			if prepareErr != nil {
				return evaluation{}, prepareErr
			}
			bytes := int64(len(historyWire))
			tokens := estimateTokens(len(historyWire), len(historyMessages))
			result.historyWireBytes = &bytes
			result.historyEstimatedTokens = &tokens
		} else {
			bytes := int64(0)
			tokens := int64(0)
			result.historyWireBytes = &bytes
			result.historyEstimatedTokens = &tokens
		}
	}
	result.request.WireBody = append([]byte(nil), wire...)
	return result, nil
}

func (p *Projector) finishWithoutHistory(
	request contracts.ModelRequest,
	current []Entry,
	currentIndices []int,
	prepare func(contracts.ModelRequest) ([]byte, error),
	historyFact HistoryFact,
) (Projection, error) {
	evaluation, err := p.evaluate(request, current, currentIndices, nil, prepare)
	if err != nil {
		return Projection{}, err
	}
	projection := p.makeProjection(evaluation, nil, currentIndices, current, historyFact)
	return projection, nil
}

func (p *Projector) projectHistory(
	ctx context.Context,
	request contracts.ModelRequest,
	current []Entry,
	currentIndices []int,
	prepare func(contracts.ModelRequest) ([]byte, error),
) (Projection, error) {
	var selected []*historySelection // newest first
	var observed []HistoryTurnFact   // newest first while reading
	first := true
	beforeTurn := int64(0)
	status := HistoryFull
	reason := ""
	var finalEvaluation evaluation

	for {
		if err := ctx.Err(); err != nil {
			return Projection{}, err
		}
		turn, err := p.history(ctx, beforeTurn)
		if err != nil {
			return Projection{}, err
		}
		if turn == nil {
			if first {
				status = HistoryNone
			}
			finalEvaluation, err = p.evaluate(request, current, currentIndices, selected, prepare)
			if err != nil {
				return Projection{}, err
			}
			return p.makeProjection(finalEvaluation, selected, currentIndices, current,
				HistoryFact{Status: status, Reason: reason, Turns: chronologicalTurnFacts(observed)}), nil
		}
		first = false

		candidate := &historySelection{
			turn:        turn,
			units:       projectionUnits(turn.Entries),
			keptIndices: allIndices(len(turn.Entries)),
			omitted:     make(map[int]struct{}),
			status:      "full",
		}
		selected = append(selected, candidate)
		candidateEvaluation, evalErr := p.evaluate(request, current, currentIndices, selected, prepare)
		if evalErr != nil {
			return Projection{}, evalErr
		}
		if candidateEvaluation.fits(p.budget) {
			observed = append(observed, historyTurnFact(candidate))
			finalEvaluation = candidateEvaluation
			beforeTurn = turn.Number
			continue
		}
		selected = selected[:len(selected)-1]

		if len(selected) == 0 {
			partial, partialEvaluation, partialFits, partialErr := p.partialHistoryTurn(
				request, current, currentIndices, candidate, prepare,
			)
			if partialErr != nil {
				return Projection{}, partialErr
			}
			if partialFits {
				selected = append(selected, partial)
				observed = append(observed, historyTurnFact(partial))
				finalEvaluation = partialEvaluation
				status = HistoryPartial
				reason = "recent_turn_trimmed"
			} else {
				candidate.status = "omitted"
				candidate.reason = "protected_history_does_not_fit"
				candidate.omitted = indexSet(allIndices(len(turn.Entries)))
				candidate.keptIndices = nil
				observed = append(observed, historyTurnFact(candidate))
				status = HistoryOmitted
				reason = candidate.reason
				finalEvaluation, evalErr = p.evaluate(request, current, currentIndices, nil, prepare)
				if evalErr != nil {
					return Projection{}, evalErr
				}
			}
		} else {
			candidate.status = "omitted"
			candidate.reason = "older_turn_does_not_fit"
			candidate.omitted = indexSet(allIndices(len(turn.Entries)))
			candidate.keptIndices = nil
			observed = append(observed, historyTurnFact(candidate))
			status = HistoryPartial
			reason = candidate.reason
			finalEvaluation, evalErr = p.evaluate(request, current, currentIndices, selected, prepare)
			if evalErr != nil {
				return Projection{}, evalErr
			}
		}
		break
	}

	return p.makeProjection(finalEvaluation, selected, currentIndices, current,
		HistoryFact{Status: status, Reason: reason, Turns: chronologicalTurnFacts(observed)}), nil
}

func (p *Projector) partialHistoryTurn(
	request contracts.ModelRequest,
	current []Entry,
	currentIndices []int,
	candidate *historySelection,
	prepare func(contracts.ModelRequest) ([]byte, error),
) (*historySelection, evaluation, bool, error) {
	protected := protectedHistoryIndices(candidate.turn.Entries, candidate.units)
	partial := &historySelection{
		turn:        candidate.turn,
		units:       candidate.units,
		keptIndices: allIndices(len(candidate.turn.Entries)),
		omitted:     make(map[int]struct{}),
		status:      "partial",
		reason:      "recent_turn_trimmed",
	}
	for _, unit := range oldestRemovableUnits(candidate.units, protected) {
		removeUnit(partial, unit)
		evaluation, err := p.evaluate(request, current, currentIndices, []*historySelection{partial}, prepare)
		if err != nil {
			return nil, evaluation, false, err
		}
		if evaluation.fits(p.budget) {
			return partial, evaluation, true, nil
		}
	}
	return nil, evaluation{}, false, nil
}

func (p *Projector) makeProjection(
	evaluation evaluation,
	history []*historySelection,
	currentIndices []int,
	current []Entry,
	historyFact HistoryFact,
) Projection {
	selectedSources := make([]Source, 0, len(evaluation.request.Messages))
	for _, entry := range p.prefix {
		selectedSources = append(selectedSources, entry.Source)
	}
	for _, selection := range chronologicalHistory(history) {
		for _, index := range selection.keptIndices {
			selectedSources = append(selectedSources, selection.turn.Entries[index].Source)
		}
	}
	for _, index := range currentIndices {
		if index >= 0 && index < len(current) {
			selectedSources = append(selectedSources, current[index].Source)
		}
	}
	omitted := make([]int, 0, len(p.omittedCurrentIndices))
	for index := range p.omittedCurrentIndices {
		omitted = append(omitted, index)
	}
	sort.Ints(omitted)
	fact := Fact{
		Budget:                 cloneBudget(p.budget),
		Estimator:              Estimator,
		WireBytes:              evaluation.wireBytes,
		EstimatedTokens:        evaluation.estimatedTokens,
		HistoryWireBytes:       cloneInt64(evaluation.historyWireBytes),
		HistoryEstimatedTokens: cloneInt64(evaluation.historyEstimatedTokens),
		Sources:                append([]Source(nil), selectedSources...),
		OmittedCurrent:         omitted,
		HistoryStatus:          historyFact.Status,
		History:                historyFact,
	}
	for _, index := range omitted {
		if index >= 0 && index < len(current) {
			fact.OmittedCurrentSources = append(fact.OmittedCurrentSources, current[index].Source)
		}
	}
	return Projection{
		Request: evaluation.request,
		Fact:    fact,
		Sources: append([]Source(nil), selectedSources...),
	}
}

func (p *Projector) currentIndices(length int) []int {
	indices := make([]int, 0, length)
	for index := 0; index < length; index++ {
		if _, omitted := p.omittedCurrentIndices[index]; !omitted {
			indices = append(indices, index)
		}
	}
	return indices
}

func chronologicalHistory(history []*historySelection) []*historySelection {
	ordered := make([]*historySelection, len(history))
	for index := range history {
		ordered[len(history)-1-index] = history[index]
	}
	return ordered
}

func chronologicalTurnFacts(turns []HistoryTurnFact) []HistoryTurnFact {
	ordered := make([]HistoryTurnFact, len(turns))
	for index := range turns {
		ordered[len(turns)-1-index] = turns[index]
	}
	return ordered
}

func historyTurnFact(selection *historySelection) HistoryTurnFact {
	fact := HistoryTurnFact{
		Number: selection.turn.Number,
		Status: selection.status,
		Reason: selection.reason,
	}
	for _, index := range selection.keptIndices {
		fact.SelectedSources = append(fact.SelectedSources, selection.turn.Entries[index].Source)
	}
	for _, index := range allIndices(len(selection.turn.Entries)) {
		if _, omitted := selection.omitted[index]; omitted {
			fact.OmittedSources = append(fact.OmittedSources, selection.turn.Entries[index].Source)
		}
	}
	return fact
}

func removeUnit(selection *historySelection, unit projectionUnit) {
	remove := indexSet(unit.indices)
	for index := range remove {
		selection.omitted[index] = struct{}{}
	}
	kept := make([]int, 0, len(selection.keptIndices)-len(remove))
	for _, index := range selection.keptIndices {
		if _, removed := remove[index]; !removed {
			kept = append(kept, index)
		}
	}
	selection.keptIndices = kept
}

func estimateTokens(wireBytes, messageCount int) int64 {
	bytes := int64(wireBytes)
	return (bytes+2)/3 + 16 + 8*int64(messageCount)
}

func allIndices(length int) []int {
	indices := make([]int, length)
	for index := range indices {
		indices[index] = index
	}
	return indices
}

func indexSet(indices []int) map[int]struct{} {
	set := make(map[int]struct{}, len(indices))
	for _, index := range indices {
		set[index] = struct{}{}
	}
	return set
}

func cloneBudget(budget Budget) Budget {
	budget.ContextTokens = cloneInt64(budget.ContextTokens)
	budget.InputTokens = cloneInt64(budget.InputTokens)
	budget.OutputReserve = cloneInt64(budget.OutputReserve)
	budget.InputRatio = cloneFloat64(budget.InputRatio)
	budget.HistoryTokens = cloneInt64(budget.HistoryTokens)
	budget.Limit = cloneInt64(budget.Limit)
	return budget
}
