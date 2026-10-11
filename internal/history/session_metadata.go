package history

import (
	"context"
	"fmt"
)

// RenameSession changes only display metadata. In particular, it leaves the
// revision captured by an admitted execution unchanged so that rename does
// not conflict with that execution's later adoption.
func (s *Store) RenameSession(ctx context.Context, id, title string) (Session, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET title=?, updated_at=? WHERE session_id=?`, title, timestampNow(), id)
	if err != nil {
		return Session{}, fmt.Errorf("history rename Session: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return Session{}, fmt.Errorf("history count renamed Sessions: %w", err)
	}
	if changed == 0 {
		return Session{}, ErrNotFound
	}
	return s.Session(ctx, id)
}
