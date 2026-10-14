package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"hjh/internal/contracts"
)

// LatestRequestSnapshot is the prepared provider body for an existing
// model_request record.
type LatestRequestSnapshot struct {
	RecordID string
	WireBody []byte
}

// LatestBudgetSnapshot is the detailed Fact for an existing context_budget
// summary record.
type LatestBudgetSnapshot struct {
	RecordID string
	Fact     json.RawMessage
}

// LatestSnapshotStatus reads the current saved/unavailable state for an
// execution without loading the snapshot bodies. Logical metadata determines
// the latest records; a snapshot row for an older or physical request does not
// make the latest detail available.
func (s *Store) LatestSnapshotStatus(ctx context.Context, executionID string) (contracts.SnapshotStatus, error) {
	var lifecycle string
	var requestSaved, budgetSaved bool
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT lifecycle FROM executions WHERE execution_id=?),''),
		EXISTS(SELECT 1 FROM semantic_records r
			WHERE r.execution_id=? AND r.kind='model_request'
				AND json_extract(r.payload_json,'$.schemaVersion')=1
				AND r.ordinal=(SELECT MAX(latest.ordinal) FROM semantic_records latest
					WHERE latest.execution_id=r.execution_id AND latest.kind='model_request'
						AND json_extract(latest.payload_json,'$.schemaVersion')=1)
				AND EXISTS(SELECT 1 FROM execution_latest_requests snapshot
					WHERE snapshot.execution_id=r.execution_id AND snapshot.record_id=r.record_id)),
		EXISTS(SELECT 1 FROM semantic_records r
			WHERE r.execution_id=? AND r.kind='context_budget'
				AND r.ordinal=(SELECT MAX(latest.ordinal) FROM semantic_records latest
					WHERE latest.execution_id=r.execution_id AND latest.kind='context_budget')
				AND EXISTS(SELECT 1 FROM execution_latest_budgets snapshot
					WHERE snapshot.execution_id=r.execution_id AND snapshot.record_id=r.record_id))`,
		executionID, executionID, executionID).Scan(&lifecycle, &requestSaved, &budgetSaved)
	if err != nil {
		return contracts.SnapshotStatus{}, fmt.Errorf("history read latest snapshot status: %w", err)
	}
	status := contracts.SnapshotStatus{ExecutionID: executionID, State: contracts.SnapshotSaveUnavailable,
		DetailAvailable: requestSaved || budgetSaved, Frozen: lifecycle != "" && lifecycle != "active"}
	if status.DetailAvailable {
		status.State = contracts.SnapshotSaveSaved
	}
	return status, nil
}

// SaveLatestSnapshots updates only the request/budget detail rows for existing
// records. It works while an execution is active or settled and does not add
// records or change execution outcome, adoption, or ordinal state.
func (s *Store) SaveLatestSnapshots(ctx context.Context, executionID string, request *LatestRequestSnapshot, budget *LatestBudgetSnapshot) error {
	if request == nil && budget == nil {
		return errors.New("history snapshots: no detail supplied")
	}
	if budget != nil && !validJSON(budget.Fact) {
		return ErrInvalidJSON
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("history begin snapshot save: %w", err)
	}
	defer tx.Rollback()

	if request != nil {
		if err := validateSnapshotRecordTx(ctx, tx, executionID, request.RecordID, "model_request"); err != nil {
			return err
		}
	}
	if budget != nil {
		if err := validateSnapshotRecordTx(ctx, tx, executionID, budget.RecordID, "context_budget"); err != nil {
			return err
		}
	}
	if request != nil {
		wireBody := request.WireBody
		if wireBody == nil {
			// A present request may have an empty body; a nil request pointer is
			// what represents the absence of request metadata.
			wireBody = []byte{}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO execution_latest_requests(execution_id,record_id,wire_body)
			VALUES(?,?,?) ON CONFLICT(execution_id) DO UPDATE SET
				record_id=excluded.record_id,wire_body=excluded.wire_body`, executionID, request.RecordID, wireBody); err != nil {
			return fmt.Errorf("history save latest request snapshot: %w", err)
		}
	}
	if budget != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO execution_latest_budgets(execution_id,record_id,fact_json)
			VALUES(?,?,?) ON CONFLICT(execution_id) DO UPDATE SET
				record_id=excluded.record_id,fact_json=excluded.fact_json`, executionID, budget.RecordID, string(budget.Fact)); err != nil {
			return fmt.Errorf("history save latest budget snapshot: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("history commit snapshot save: %w", err)
	}
	return nil
}

func validateSnapshotRecordTx(ctx context.Context, tx *sql.Tx, executionID, recordID, expectedKind string) error {
	var actualKind string
	err := tx.QueryRowContext(ctx, `SELECT kind FROM semantic_records WHERE record_id=? AND execution_id=?`, recordID, executionID).Scan(&actualKind)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("history validate snapshot record: %w", err)
	}
	if actualKind != expectedKind {
		return fmt.Errorf("history snapshot record %q has kind %q, want %q", recordID, actualKind, expectedKind)
	}
	return nil
}
