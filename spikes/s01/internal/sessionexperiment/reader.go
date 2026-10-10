package sessionexperiment

import (
	"encoding/json"
	"errors"
	"runtime"
	"time"

	"hjh/spikes/s01/internal/configsnapshot"
	"hjh/spikes/s01/internal/history"
	"hjh/spikes/s01/internal/session"
)

type Inspection struct {
	Info                 history.SessionInfo `json:"info"`
	Conversation         history.Window      `json:"conversation"`
	Input                []session.Message   `json:"messages"`
	Turns                []history.Turn      `json:"turnPage"`
	Plan                 []string            `json:"queryPlan"`
	InputBytes           int                 `json:"inputBytes"`
	WindowAllocatedBytes uint64              `json:"windowAllocatedBytes"`
	WindowDurationUS     int64               `json:"windowDurationUS"`
}

func Inspect(db, sessionID string, before, after, count int) (Inspection, error) {
	var v Inspection
	store, err := history.OpenReadOnly(db)
	if err != nil {
		return v, err
	}
	defer store.Close()
	v.Info, err = store.SessionInfo(sessionID)
	if err != nil {
		return v, err
	}
	var start, end runtime.MemStats
	runtime.ReadMemStats(&start)
	at := time.Now()
	v.Conversation, err = store.Conversation(sessionID, before, count)
	v.WindowDurationUS = time.Since(at).Microseconds()
	runtime.ReadMemStats(&end)
	v.WindowAllocatedBytes = end.TotalAlloc - start.TotalAlloc
	if err != nil {
		return v, err
	}
	v.Input = session.Input(configsnapshot.Settings{}, v.Conversation).Messages
	data, err := json.Marshal(v.Input)
	if err != nil {
		return v, err
	}
	v.InputBytes = len(data)
	v.Turns, err = store.Turns(sessionID, after, count)
	if err != nil {
		return v, err
	}
	v.Plan, err = store.ConversationPlan(sessionID, before, count)
	return v, err
}

func open(db string) (*history.Store, error) {
	s, err := history.Open(db)
	if err != nil {
		return nil, err
	}
	if err = s.InitSessions(); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	return s, nil
}
