package app

import (
	"encoding/json"

	"hjh/internal/contracts"
	"hjh/internal/history"
)

func (s *Service) mergeSnapshotRecord(record *history.Record, capture SnapshotCapture, hasCapture bool) error {
	if record.SnapshotSave == nil {
		return nil
	}
	status := *record.SnapshotSave
	if hasCapture && capture.ExecutionID == record.ExecutionID {
		switch record.Kind {
		case "model_request":
			if capture.Request != nil && capture.Request.RecordID == record.ID {
				if capture.RequestStatus.State == contracts.SnapshotSaveMemory || capture.RequestStatus.State == contracts.SnapshotSaveFailed {
					status = capture.RequestStatus
					record.Content = make([]byte, len(capture.Request.WireBody))
					copy(record.Content, capture.Request.WireBody)
				}
			}
		case "context_budget":
			if capture.Budget != nil && capture.Budget.RecordID == record.ID {
				if capture.BudgetStatus.State == contracts.SnapshotSaveMemory || capture.BudgetStatus.State == contracts.SnapshotSaveFailed {
					fact, err := json.Marshal(capture.Budget.Fact)
					if err != nil {
						return err
					}
					status = capture.BudgetStatus
					record.Payload, err = recordSnapshotPayload(record.Payload, status, fact)
					if err != nil {
						return err
					}
				}
			}
		}
	}
	record.SnapshotSave = &status
	payload, err := recordSnapshotPayload(record.Payload, status, nil)
	if err != nil {
		return err
	}
	record.Payload = payload
	return nil
}

func recordSnapshotPayload(payload json.RawMessage, status contracts.SnapshotStatus, fact json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, err
	}
	fields["snapshotSave"] = contracts.JSON(status)
	fields["detailAvailable"] = contracts.JSON(status.DetailAvailable)
	if fact != nil {
		fields["fact"] = append(json.RawMessage(nil), fact...)
	}
	return json.Marshal(fields)
}

func (s *Service) mergeSessionContextSnapshots(result *contracts.SessionContext) bool {
	refresh := false
	if result.LatestRequest != nil {
		capture, ok := s.snapshotOwner.capture(result.LatestRequest.ExecutionID)
		if ok && capture.Request != nil && capture.Request.RecordID == result.LatestRequest.RecordID {
			status := capture.RequestStatus
			if status.State == contracts.SnapshotSaveMemory || status.State == contracts.SnapshotSaveFailed {
				result.LatestRequest.SnapshotSave = status
			} else if status.State == contracts.SnapshotSaveSaved && result.LatestRequest.SnapshotSave.State != contracts.SnapshotSaveSaved {
				refresh = true
			}
		}
	}
	if result.LatestBudget != nil {
		capture, ok := s.snapshotOwner.capture(result.LatestBudget.ExecutionID)
		if ok && capture.Budget != nil && capture.Budget.RecordID == result.LatestBudget.RecordID {
			status := capture.BudgetStatus
			if status.State == contracts.SnapshotSaveMemory || status.State == contracts.SnapshotSaveFailed {
				result.LatestBudget.Fact = contracts.JSON(capture.Budget.Fact)
				result.LatestBudget.DetailAvailable = true
				result.LatestBudget.SnapshotSave = status
			} else if status.State == contracts.SnapshotSaveSaved && result.LatestBudget.SnapshotSave.State != contracts.SnapshotSaveSaved {
				refresh = true
			}
		}
	}
	return refresh
}
