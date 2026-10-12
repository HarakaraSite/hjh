// Package history owns SQLite persistence for Sessions, executions, messages,
// configurations, and semantic execution records.
package history

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"hjh/internal/contracts"
	_ "modernc.org/sqlite"
)

const schemaVersion = 3

//go:embed schema.sql
var schemaFile embed.FS

var (
	ErrNotFound          = errors.New("history: row not found")
	ErrInvalidJSON       = errors.New("history: invalid JSON")
	ErrRevisionConflict  = errors.New("history: Session revision changed before adoption")
	ErrExecutionClosed   = errors.New("history: execution is not active")
	ErrRecallAmbiguous   = errors.New("history: recall selector matches multiple executions")
	ErrRecallNotFound    = errors.New("history: recall source not found")
	ErrCommandIDConflict = errors.New("history: command ID conflicts with an existing operation")
)

type Store struct {
	db *sql.DB
}

// Open opens a history database. The schema is installed only when the path
// did not exist before this call; existing databases are opened as-is.
func Open(path string) (*Store, error) {
	isNew := path == ":memory:"
	if path != ":memory:" {
		_, err := os.Stat(path)
		isNew = errors.Is(err, os.ErrNotExist)
		if err != nil && !isNew {
			return nil, fmt.Errorf("history stat: %w", err)
		}
	}
	store, err := open(path, false)
	if err != nil {
		return nil, err
	}
	if isNew {
		if err := store.installSchema(context.Background()); err != nil {
			_ = store.Close()
			return nil, err
		}
	} else if err := store.checkSchema(context.Background()); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

// OpenReadOnly opens an existing history database without creating or
// initializing it.
func OpenReadOnly(path string) (*Store, error) {
	store, err := open(path, true)
	if err != nil {
		return nil, err
	}
	if err := store.checkSchema(context.Background()); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

func open(path string, readOnly bool) (*Store, error) {
	dsn := path
	if path == ":memory:" {
		query := url.Values{}
		query.Add("_pragma", "foreign_keys(1)")
		query.Add("_pragma", "busy_timeout(5000)")
		if !readOnly {
			query.Set("_txlock", "immediate")
		}
		dsn = "file::memory:?" + query.Encode()
	} else {
		fileURL := &url.URL{Scheme: "file", Path: path}
		query := fileURL.Query()
		if readOnly {
			query.Set("mode", "ro")
		}
		query.Add("_pragma", "foreign_keys(1)")
		query.Add("_pragma", "busy_timeout(5000)")
		if !readOnly {
			query.Set("_txlock", "immediate")
		}
		absolutePath, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("history resolve database path: %w", err)
		}
		fileURL.Path = absolutePath
		fileURL.RawQuery = query.Encode()
		dsn = fileURL.String()
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("history open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("history connect: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) installSchema(ctx context.Context) error {
	schema, err := schemaFile.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("history read embedded schema: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("history begin schema transaction: %w", err)
	}
	if _, err := tx.ExecContext(ctx, string(schema)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("history install schema: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("history commit schema: %w", err)
	}
	return nil
}

func (s *Store) checkSchema(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("history read schema version: %w", err)
	}
	if version != schemaVersion {
		return fmt.Errorf("history schema version %d, want %d", version, schemaVersion)
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

type SessionInput struct {
	ID            string
	WorkspaceRoot string
	AgentChoice   json.RawMessage
	ActiveModel   json.RawMessage
	Title         *string
	CreatedAt     string
}

type Session struct {
	ID                   string
	WorkspaceRoot        string
	AgentChoice          json.RawMessage
	Agent                string
	CreatedAt            string
	UpdatedAt            string
	Title                *string
	StateRevision        int64
	NextTurn             int64
	ActiveModel          json.RawMessage
	MessageCount         int64
	ModelChangeCount     int64
	TurnCount            int64
	PrivateStateFromTurn int64
}

func (s *Store) CreateSession(ctx context.Context, input SessionInput) (Session, error) {
	if !validJSON(input.AgentChoice) || !validJSON(input.ActiveModel) {
		return Session{}, ErrInvalidJSON
	}
	id := input.ID
	if id == "" {
		id = uuid.NewString()
	}
	createdAt := input.CreatedAt
	if createdAt == "" {
		createdAt = timestampNow()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("history begin Session: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO sessions(
			session_id, workspace_root, agent_choice_json, created_at, updated_at, title,
			state_revision, next_turn, active_model_json, message_count, model_change_count,
			turn_count, private_state_from_turn, checkpoint_json
		) VALUES(?, ?, ?, ?, ?, ?, 1, 1, ?, 0, 1, 0, 1, NULL)`,
		id, input.WorkspaceRoot, string(input.AgentChoice), createdAt, createdAt,
		nullableString(input.Title), string(input.ActiveModel),
	)
	if err != nil {
		return Session{}, fmt.Errorf("history insert Session: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO session_model_changes(
			session_id, change_ordinal, effective_from_turn, changed_at, selection_json
		) VALUES(?, 0, 1, ?, ?)`, id, createdAt, string(input.ActiveModel))
	if err != nil {
		return Session{}, fmt.Errorf("history insert initial model selection: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("history commit Session: %w", err)
	}
	return s.Session(ctx, id)
}

func (s *Store) Session(ctx context.Context, id string) (Session, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT session_id, workspace_root, agent_choice_json, created_at, updated_at, title,
			state_revision, next_turn, active_model_json, message_count, model_change_count,
			turn_count, private_state_from_turn
		FROM sessions WHERE session_id=?`, id)
	var session Session
	var title sql.NullString
	var choiceJSON, modelJSON string
	if err := row.Scan(
		&session.ID, &session.WorkspaceRoot, &choiceJSON, &session.CreatedAt,
		&session.UpdatedAt, &title, &session.StateRevision, &session.NextTurn,
		&modelJSON, &session.MessageCount, &session.ModelChangeCount,
		&session.TurnCount, &session.PrivateStateFromTurn,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, fmt.Errorf("history read Session: %w", err)
	}
	if title.Valid {
		session.Title = &title.String
	}
	session.AgentChoice = json.RawMessage(choiceJSON)
	session.ActiveModel = json.RawMessage(modelJSON)
	session.Agent = agentName(session.AgentChoice)
	return session, nil
}

func (s *Store) ListSessions(ctx context.Context) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, workspace_root, agent_choice_json, created_at, updated_at, title,
			state_revision, next_turn, active_model_json, message_count, model_change_count,
			turn_count, private_state_from_turn
		FROM sessions ORDER BY updated_at DESC, session_id DESC`)
	if err != nil {
		return nil, fmt.Errorf("history list Sessions: %w", err)
	}
	defer rows.Close()
	var sessions []Session
	for rows.Next() {
		var session Session
		var title sql.NullString
		var choiceJSON, modelJSON string
		if err := rows.Scan(
			&session.ID, &session.WorkspaceRoot, &choiceJSON,
			&session.CreatedAt, &session.UpdatedAt, &title, &session.StateRevision,
			&session.NextTurn, &modelJSON, &session.MessageCount,
			&session.ModelChangeCount, &session.TurnCount,
			&session.PrivateStateFromTurn,
		); err != nil {
			return nil, fmt.Errorf("history scan Session: %w", err)
		}
		if title.Valid {
			session.Title = &title.String
		}
		session.AgentChoice = json.RawMessage(choiceJSON)
		session.ActiveModel = json.RawMessage(modelJSON)
		session.Agent = agentName(session.AgentChoice)
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history list Sessions: %w", err)
	}
	return sessions, nil
}

// Execution reads one execution by its ID, including its task bytes and the
// raw model/build JSON captured when it began.
func (s *Store) Execution(ctx context.Context, id string) (Execution, error) {
	return s.readExecution(ctx, id)
}

// UpdateSessionModel records a model selection change effective on the next
// turn and advances the Session revision used by execution admission.
func (s *Store) UpdateSessionModel(ctx context.Context, sessionID string, selection json.RawMessage, changedAt string) (Session, error) {
	if !validJSON(selection) {
		return Session{}, ErrInvalidJSON
	}
	if changedAt == "" {
		changedAt = timestampNow()
	}
	encodedSelection, err := contracts.EncodeCanonicalJSON(selection)
	if err != nil {
		return Session{}, fmt.Errorf("history canonicalize model selection: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("history begin Session model update: %w", err)
	}
	defer tx.Rollback()
	var currentJSON string
	var changeCount, nextTurn, privateStateFromTurn, revision int64
	if err := tx.QueryRowContext(ctx, `
		SELECT active_model_json, model_change_count, next_turn, private_state_from_turn, state_revision
		FROM sessions WHERE session_id=?`, sessionID,
	).Scan(&currentJSON, &changeCount, &nextTurn, &privateStateFromTurn, &revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, fmt.Errorf("history read Session model: %w", err)
	}
	currentCanonical, err := contracts.EncodeCanonicalJSON(json.RawMessage(currentJSON))
	if err != nil {
		return Session{}, fmt.Errorf("history canonicalize stored model selection: %w", err)
	}
	if bytes.Equal(currentCanonical, encodedSelection) {
		if err := tx.Commit(); err != nil {
			return Session{}, fmt.Errorf("history commit unchanged Session model: %w", err)
		}
		return s.Session(ctx, sessionID)
	}
	var current, updated struct {
		Provider string `json:"provider"`
		ModelID  string `json:"modelId"`
	}
	if err := json.Unmarshal([]byte(currentJSON), &current); err != nil {
		return Session{}, fmt.Errorf("history decode stored model selection: %w", err)
	}
	if err := json.Unmarshal(selection, &updated); err != nil {
		return Session{}, fmt.Errorf("history decode model selection: %w", err)
	}
	if current.Provider != updated.Provider || current.ModelID != updated.ModelID {
		privateStateFromTurn = nextTurn
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO session_model_changes(
			session_id, change_ordinal, effective_from_turn, changed_at, selection_json
		) VALUES(?, ?, ?, ?, ?)`, sessionID, changeCount, nextTurn, changedAt, string(selection),
	); err != nil {
		return Session{}, fmt.Errorf("history append Session model change: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE sessions SET active_model_json=?, model_change_count=model_change_count+1,
			private_state_from_turn=?, state_revision=state_revision+1, updated_at=?
		WHERE session_id=? AND state_revision=?`,
		string(selection), privateStateFromTurn, changedAt, sessionID, revision,
	); err != nil {
		return Session{}, fmt.Errorf("history update Session model: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("history commit Session model: %w", err)
	}
	return s.Session(ctx, sessionID)
}

type BeginExecutionInput struct {
	ExecutionID       string
	Task              string
	TaskContentDigest string
	TaskID            string
	SessionID         string
	AgentName         string
	Model             json.RawMessage
	Build             json.RawMessage
	ConfigurationID   string
	MaxSteps          int64
	CreatedAt         string
	Recall            *contracts.RecallReference
}

type BeginChildExecutionInput struct {
	ExecutionID       string
	TaskID            string
	Task              string
	ParentExecutionID string
	SpawnCallID       string
	AgentName         string
	Model             json.RawMessage
	Build             json.RawMessage
	ConfigurationID   string
	MaxSteps          int64
	CreatedAt         string
}

type Execution struct {
	ID                 string
	TaskID             string
	Task               string
	SessionID          string
	SessionCorrelation string
	ParentExecutionID  string
	SpawnCallID        string
	Turn               int64
	BaseRevision       int64
	BaseMessageCount   int64
	CreatedAt          string
	SettledAt          *string
	AgentName          string
	Model              json.RawMessage
	Build              json.RawMessage
	ConfigurationID    string
	MaxSteps           int64
	Lifecycle          string
	Outcome            string
	Adoption           string
	TerminalRecordID   string
	RecallRecordID     string
}

func (s *Store) BeginExecution(ctx context.Context, input BeginExecutionInput) (Execution, error) {
	if !validJSON(input.Model) || !validJSON(input.Build) {
		return Execution{}, ErrInvalidJSON
	}
	executionID := input.ExecutionID
	if executionID == "" {
		executionID = uuid.NewString()
	}
	taskID := input.TaskID
	if taskID == "" {
		taskID = uuid.NewString()
	}
	createdAt := input.CreatedAt
	if createdAt == "" {
		createdAt = timestampNow()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Execution{}, fmt.Errorf("history begin execution: %w", err)
	}
	defer tx.Rollback()
	var baseRevision, baseMessageCount, turn int64
	var choiceJSON string
	if err := tx.QueryRowContext(ctx, `
		SELECT state_revision, message_count, next_turn, agent_choice_json
		FROM sessions WHERE session_id=?`, input.SessionID,
	).Scan(&baseRevision, &baseMessageCount, &turn, &choiceJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Execution{}, ErrNotFound
		}
		return Execution{}, fmt.Errorf("history read Session for execution: %w", err)
	}
	configurationID := any(input.ConfigurationID)
	if input.ConfigurationID == "" {
		configurationID = nil
	}
	if input.ConfigurationID != "" {
		var exists int
		if err := tx.QueryRowContext(ctx,
			"SELECT 1 FROM configurations WHERE configuration_id=?", input.ConfigurationID,
		).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Execution{}, fmt.Errorf("history configuration: %w", ErrNotFound)
			}
			return Execution{}, fmt.Errorf("history read configuration: %w", err)
		}
	}
	agent := input.AgentName
	if agent == "" {
		agent = agentName(json.RawMessage(choiceJSON))
	}
	taskDigest := input.TaskContentDigest
	task := input.Task
	if taskDigest == "" {
		taskDigest, err = putContent(ctx, tx, []byte(input.Task))
		if err != nil {
			return Execution{}, err
		}
	} else {
		var storedTask []byte
		if err := tx.QueryRowContext(ctx, "SELECT content_bytes FROM contents WHERE content_digest=?", taskDigest).Scan(&storedTask); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Execution{}, ErrNotFound
			}
			return Execution{}, fmt.Errorf("history read execution task content: %w", err)
		}
		task = string(storedTask)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO executions(
			execution_id, canonical_session_id, session_correlation, task_id,
			task_content_digest, turn_number, base_revision, base_message_count,
			created_at, agent_name, configuration_id, build_json, model_json, max_steps,
			lifecycle, outcome, adoption, latest_ordinal, occurrence_count,
			unresolved_mandatory_count, event_count
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', 'unknown',
			'non_canonical', 0, 0, 0, 0)`,
		executionID, input.SessionID, input.SessionID, taskID, taskDigest, turn,
		baseRevision, baseMessageCount, createdAt, agent, configurationID,
		string(input.Build), string(input.Model), input.MaxSteps,
	)
	if err != nil {
		return Execution{}, fmt.Errorf("history insert execution: %w", err)
	}
	var displayOrdinal int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(execution_ordinal), -1) + 1
		FROM execution_display_positions WHERE session_correlation=?`, input.SessionID,
	).Scan(&displayOrdinal); err != nil {
		return Execution{}, fmt.Errorf("history allocate execution position: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO execution_display_positions(session_correlation, execution_ordinal, execution_id)
		VALUES(?, ?, ?)`, input.SessionID, displayOrdinal, executionID,
	); err != nil {
		return Execution{}, fmt.Errorf("history save execution position: %w", err)
	}
	var recallRecordID string
	if input.Recall != nil {
		reference := *input.Recall
		if reference.CoreID == "" || reference.OperationID == "" || reference.SourceExecutionID == "" {
			return Execution{}, fmt.Errorf("history begin execution: incomplete recall reference")
		}
		var sourceLifecycle, sourceOutcome, sourceAdoption, sourceCorrelation string
		if err := tx.QueryRowContext(ctx, `SELECT lifecycle,outcome,adoption,session_correlation
			FROM executions WHERE execution_id=?`, reference.SourceExecutionID).Scan(
			&sourceLifecycle, &sourceOutcome, &sourceAdoption, &sourceCorrelation,
		); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Execution{}, ErrRecallNotFound
			}
			return Execution{}, fmt.Errorf("history read recall source for admission: %w", err)
		}
		if sourceLifecycle != "settled" || sourceAdoption != "non_canonical" || sourceCorrelation != input.SessionID ||
			!recallOutcome(sourceOutcome) {
			return Execution{}, ErrRecallNotFound
		}
		ordinal := int64(1)
		recallRecordID = semanticRecordID(executionID, ordinal)
		recallPayload := contracts.JSON(map[string]any{
			"schemaVersion": 1, "sourceExecutionId": reference.SourceExecutionID,
			"targetExecutionId": executionID, "coreId": reference.CoreID,
			"operationId": reference.OperationID, "generation": reference.Generation,
		})
		encoded, err := contracts.EncodeCanonicalJSON(recallPayload)
		if err != nil {
			return Execution{}, fmt.Errorf("history encode recall relation: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO semantic_records(
			record_id,execution_id,ordinal,kind,observed_at,payload_json,content_digest
		) VALUES(?,?,?,'recall_relation',?,?,NULL)`, recallRecordID, executionID, ordinal, createdAt, string(encoded)); err != nil {
			return Execution{}, fmt.Errorf("history save recall relation record: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO recall_relations(source_execution_id,target_execution_id,record_id)
			VALUES(?,?,?)`, reference.SourceExecutionID, executionID, recallRecordID); err != nil {
			return Execution{}, fmt.Errorf("history save recall relation: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE executions SET latest_ordinal=?,occurrence_count=occurrence_count+1
			WHERE execution_id=?`, ordinal, executionID); err != nil {
			return Execution{}, fmt.Errorf("history update recall relation position: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Execution{}, fmt.Errorf("history commit execution: %w", err)
	}
	// Return the committed admission from values already captured in the write
	// transaction. A cancelled caller must not lose the commit result to a
	// cancellable post-commit readback.
	result := Execution{ID: executionID, TaskID: taskID, Task: task, SessionID: input.SessionID,
		SessionCorrelation: input.SessionID, Turn: turn, BaseRevision: baseRevision,
		BaseMessageCount: baseMessageCount, CreatedAt: createdAt, AgentName: agent,
		Model: append(json.RawMessage(nil), input.Model...), Build: append(json.RawMessage(nil), input.Build...),
		ConfigurationID: input.ConfigurationID, MaxSteps: input.MaxSteps, Lifecycle: "active",
		Outcome: "unknown", Adoption: "non_canonical", RecallRecordID: recallRecordID}
	return result, nil
}

// BeginChildExecution admits an independent execution under an existing
// execution. It inherits the Session correlation and turn for browsing, while
// leaving the canonical Session state untouched.
func (s *Store) BeginChildExecution(ctx context.Context, input BeginChildExecutionInput) (Execution, error) {
	if !validJSON(input.Model) || !validJSON(input.Build) {
		return Execution{}, ErrInvalidJSON
	}
	executionID := input.ExecutionID
	if executionID == "" {
		executionID = uuid.NewString()
	}
	taskID := input.TaskID
	if taskID == "" {
		taskID = uuid.NewString()
	}
	createdAt := input.CreatedAt
	if createdAt == "" {
		createdAt = timestampNow()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Execution{}, fmt.Errorf("history begin child execution: %w", err)
	}
	defer tx.Rollback()
	var sessionCorrelation string
	var turn int64
	if err := tx.QueryRowContext(ctx, `
		SELECT session_correlation, turn_number FROM executions WHERE execution_id=?`,
		input.ParentExecutionID,
	).Scan(&sessionCorrelation, &turn); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Execution{}, ErrNotFound
		}
		return Execution{}, fmt.Errorf("history read parent execution: %w", err)
	}
	configurationID := any(input.ConfigurationID)
	if input.ConfigurationID == "" {
		configurationID = nil
	}
	if input.ConfigurationID != "" {
		var exists int
		if err := tx.QueryRowContext(ctx,
			"SELECT 1 FROM configurations WHERE configuration_id=?", input.ConfigurationID,
		).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Execution{}, fmt.Errorf("history configuration: %w", ErrNotFound)
			}
			return Execution{}, fmt.Errorf("history read configuration: %w", err)
		}
	}
	taskDigest, err := putContent(ctx, tx, []byte(input.Task))
	if err != nil {
		return Execution{}, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO executions(
			execution_id, canonical_session_id, session_correlation, task_id,
			task_content_digest, parent_execution_id, spawn_call_id,
			turn_number, base_revision, base_message_count, created_at, agent_name,
			configuration_id, build_json, model_json, max_steps, lifecycle, outcome,
			adoption, latest_ordinal, occurrence_count, unresolved_mandatory_count, event_count
		) VALUES(?, NULL, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?, ?, ?, ?, ?,
			'active', 'unknown', 'non_canonical', 0, 0, 0, 0)`,
		executionID, sessionCorrelation, taskID, taskDigest, input.ParentExecutionID,
		input.SpawnCallID, turn, createdAt, input.AgentName, configurationID,
		string(input.Build), string(input.Model), input.MaxSteps,
	)
	if err != nil {
		return Execution{}, fmt.Errorf("history insert child execution: %w", err)
	}
	var displayOrdinal int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(execution_ordinal), -1) + 1
		FROM execution_display_positions WHERE session_correlation=?`, sessionCorrelation,
	).Scan(&displayOrdinal); err != nil {
		return Execution{}, fmt.Errorf("history allocate child execution position: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO execution_display_positions(session_correlation, execution_ordinal, execution_id)
		VALUES(?, ?, ?)`, sessionCorrelation, displayOrdinal, executionID,
	); err != nil {
		return Execution{}, fmt.Errorf("history save child execution position: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Execution{}, fmt.Errorf("history commit child execution: %w", err)
	}
	return s.readExecution(ctx, executionID)
}

func (s *Store) AppendMessage(ctx context.Context, executionID string, message json.RawMessage) (int64, error) {
	if !validJSON(message) {
		return 0, ErrInvalidJSON
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("history begin message append: %w", err)
	}
	defer tx.Rollback()
	var lifecycle string
	if err := tx.QueryRowContext(ctx,
		"SELECT lifecycle FROM executions WHERE execution_id=?", executionID,
	).Scan(&lifecycle); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("history read execution for message: %w", err)
	}
	if lifecycle != "active" {
		return 0, ErrExecutionClosed
	}
	var ordinal int64
	if err := tx.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(message_ordinal), -1) + 1 FROM messages WHERE execution_id=?", executionID,
	).Scan(&ordinal); err != nil {
		return 0, fmt.Errorf("history allocate message ordinal: %w", err)
	}
	digest, err := putContent(ctx, tx, message)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO messages(execution_id, message_ordinal, content_digest) VALUES(?, ?, ?)",
		executionID, ordinal, digest,
	); err != nil {
		return 0, fmt.Errorf("history append message: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("history commit message: %w", err)
	}
	return ordinal, nil
}

func validJSON(raw []byte) bool { return len(raw) > 0 && json.Valid(raw) }

func timestampNow() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

func agentName(raw json.RawMessage) string {
	var choice struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &choice) == nil && choice.Name != "" {
		return choice.Name
	}
	return "default"
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func putContent(ctx context.Context, tx *sql.Tx, content []byte) (string, error) {
	digest := contentDigest(content)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO contents(content_digest, byte_length, content_bytes)
		VALUES(?, ?, ?) ON CONFLICT(content_digest) DO NOTHING`,
		digest, len(content), content,
	); err != nil {
		return "", fmt.Errorf("history save content: %w", err)
	}
	return digest, nil
}

func contentDigest(content []byte) string {
	digestBytes := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(digestBytes[:])
}
