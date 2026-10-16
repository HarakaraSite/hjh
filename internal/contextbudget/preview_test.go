package contextbudget

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"hjh/internal/contracts"
)

func TestPreviewShowsPreselectionPressureWithoutMutatingSelection(t *testing.T) {
	messages := []contracts.Message{contracts.User("task"), contracts.Assistant(strings.Repeat("old ", 300), nil), contracts.Assistant("latest", nil)}
	current := entries(messages)
	limit := estimateTokens(len(preparedWire(testRequest([]contracts.Message{messages[0], messages[2]}))), 2)
	projector := New(Budget{}, nil, nil)
	preview, err := projector.Preview(t.Context(), Budget{Limit: &limit}, testRequest(nil), current, prepareJSON)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Candidate.Fits || preview.Candidate.MessageCount != 3 || !preview.Selected().Fits || !preview.Omitted() {
		t.Fatalf("pressure was hidden: %+v", preview)
	}
	full, err := projector.Project(t.Context(), testRequest(nil), current, prepareJSON)
	if err != nil || !reflect.DeepEqual(full.Request.Messages, messages) || len(projector.omittedCurrentIndices) != 0 || projector.currentTrimmingStarted {
		t.Fatalf("preview changed real selection: %+v %v", full, err)
	}
}

func TestPreviewIncludesRecentHistoryAndStopsAtFirstOverflow(t *testing.T) {
	recent := &Turn{Number: 9, Entries: entries([]contracts.Message{contracts.User("recent"), contracts.Assistant("answer", nil)})}
	older := &Turn{Number: 8, Entries: entries([]contracts.Message{contracts.User(strings.Repeat("older ", 300)), contracts.Assistant("older answer", nil)})}
	reads := []int64{}
	reader := func(_ context.Context, before int64) (*Turn, error) {
		reads = append(reads, before)
		if before == 0 {
			return recent, nil
		}
		if before == 9 {
			return older, nil
		}
		t.Fatal("read history beyond overflow")
		return nil, nil
	}
	current := entries([]contracts.Message{contracts.User("next")})
	selected := []contracts.Message{recent.Entries[0].Message, recent.Entries[1].Message, current[0].Message}
	limit := estimateTokens(len(preparedWire(testRequest(selected))), len(selected))
	preview, err := New(Budget{Limit: &limit}, reader, nil).Preview(t.Context(), Budget{Limit: &limit}, testRequest(nil), current, prepareJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reads, []int64{0, 9}) || preview.Candidate.Fits || preview.Candidate.MessageCount != 5 || preview.Selected().MessageCount != 3 || !preview.Omitted() {
		t.Fatalf("unexpected bounded preview: %+v, reads %v", preview, reads)
	}
}

func TestOverrideReusesBudgetFormulaAndDoesNotChangeBase(t *testing.T) {
	base := Resolve(Configuration{Defaults: Values{ContextTokens: int64Pointer(10000), InputTokens: int64Pointer(3000)}}, contracts.ModelSelection{}, Capacity{})
	raised := Override(base, Values{InputTokens: int64Pointer(7000), InputRatio: float64Pointer(0.9), OutputReserve: int64Pointer(2000)})
	if *raised.Limit != 7000 || raised.Sources.InputTokens != "hook" || *base.Limit != 3000 || *base.InputRatio != 0.9 {
		t.Fatalf("override failed: %+v base %+v", raised, base)
	}
	lowered := Override(raised, Values{InputTokens: int64Pointer(1000), HistoryTokens: int64Pointer(400)})
	if *lowered.Limit != 1000 || *lowered.HistoryTokens != 400 || *raised.Limit != 7000 {
		t.Fatalf("lower override failed: %+v", lowered)
	}
}
