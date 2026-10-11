package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PreviousConversationTurn looks up only the newest adopted turn before the
// bound. Body expansion is separate so Core can stop reading when budget fills.
func (s *Store) PreviousConversationTurn(ctx context.Context, sessionID string, from, before int64) (int64, error) {
	query := `SELECT turn_number FROM conversation_messages WHERE session_id=?`
	args := []any{sessionID}
	if from > 0 {
		query += " AND turn_number>=?"
		args = append(args, from)
	}
	if before > 0 {
		query += " AND turn_number<?"
		args = append(args, before)
	}
	query += " ORDER BY turn_number DESC LIMIT 1"
	var turn int64
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&turn); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("history previous conversation turn: %w", err)
	}
	return turn, nil
}
