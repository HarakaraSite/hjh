package history

import (
	"context"
	"database/sql"
	"fmt"

	"hjh/internal/contracts"
)

// AssistantReports reads saved visible assistant bodies and their watermark
// from one SQL statement. The cursor covers messages, including non-report
// messages, without transferring tool bodies or provider replay into Go.
func (s *Store) AssistantReports(ctx context.Context, executionID string, after int64) ([]contracts.AgentReport, int64, error) {
	rows, err := s.db.QueryContext(ctx, `WITH cut AS (
        SELECT COALESCE(MAX(message_ordinal),-1) AS upper_ordinal FROM messages WHERE execution_id=?
    ), bodies AS (
        SELECT m.message_ordinal,
        CASE WHEN json_type(CAST(c.content_bytes AS TEXT),'$.text')='text'
            THEN json_extract(CAST(c.content_bytes AS TEXT),'$.text')
            WHEN json_extract(CAST(c.content_bytes AS TEXT),'$.content.kind')='text'
            THEN json_extract(CAST(c.content_bytes AS TEXT),'$.content.text')
            ELSE '' END AS body
        FROM messages m JOIN contents c ON c.content_digest=m.content_digest
        WHERE m.execution_id=? AND m.message_ordinal>?
            AND json_extract(CAST(c.content_bytes AS TEXT),'$.role')='assistant'
    ) SELECT cut.upper_ordinal, bodies.message_ordinal, bodies.body
      FROM cut LEFT JOIN bodies ON bodies.message_ordinal<=cut.upper_ordinal AND bodies.body<>''
      ORDER BY bodies.message_ordinal`, executionID, executionID, after)
	if err != nil {
		return nil, 0, fmt.Errorf("history read assistant reports: %w", err)
	}
	defer rows.Close()
	reports := []contracts.AgentReport{}
	cursor := int64(-1)
	for rows.Next() {
		var ordinal sql.NullInt64
		var body sql.NullString
		if err := rows.Scan(&cursor, &ordinal, &body); err != nil {
			return nil, 0, fmt.Errorf("history scan assistant report: %w", err)
		}
		if ordinal.Valid && body.Valid {
			reports = append(reports, contracts.AgentReport{ExecutionID: executionID, MessageOrdinal: ordinal.Int64, Text: body.String})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("history read assistant reports: %w", err)
	}
	return reports, cursor, nil
}
