
CREATE TABLE store_metadata (
  singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
  schema_version INTEGER NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE contents (
  content_digest TEXT PRIMARY KEY,
  byte_length INTEGER NOT NULL,
  content_bytes BLOB NOT NULL
);

CREATE TABLE sessions (
  session_id TEXT PRIMARY KEY,
  workspace_root TEXT NOT NULL,
  agent_choice_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  title TEXT,
  state_revision INTEGER NOT NULL,
  next_turn INTEGER NOT NULL,
  active_model_json TEXT NOT NULL,
  message_count INTEGER NOT NULL,
  model_change_count INTEGER NOT NULL,
  turn_count INTEGER NOT NULL,
  private_state_from_turn INTEGER NOT NULL,
  checkpoint_json TEXT
);

CREATE TABLE configurations (
  configuration_id TEXT PRIMARY KEY,
  created_at TEXT NOT NULL,
  snapshot_content_digest TEXT NOT NULL REFERENCES contents(content_digest)
);

CREATE TABLE executions (
  execution_id TEXT PRIMARY KEY,
  canonical_session_id TEXT REFERENCES sessions(session_id),
  session_correlation TEXT NOT NULL,
  task_id TEXT NOT NULL,
  task_content_digest TEXT NOT NULL REFERENCES contents(content_digest),
  parent_execution_id TEXT REFERENCES executions(execution_id),
  spawn_call_id TEXT,
  turn_number INTEGER NOT NULL,
  base_revision INTEGER NOT NULL,
  base_message_count INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  settled_at TEXT,
  agent_name TEXT NOT NULL,
  configuration_id TEXT REFERENCES configurations(configuration_id),
  build_json TEXT NOT NULL,
  model_json TEXT NOT NULL,
  max_steps INTEGER NOT NULL,
  instance_correlation TEXT,
  worker_generation TEXT,
  lifecycle TEXT NOT NULL,
  outcome TEXT NOT NULL,
  adoption TEXT NOT NULL,
  latest_ordinal INTEGER NOT NULL,
  occurrence_count INTEGER NOT NULL,
  terminal_record_id TEXT REFERENCES semantic_records(record_id),
  unresolved_mandatory_count INTEGER NOT NULL,
  event_count INTEGER NOT NULL,
  runtime_outcome_json TEXT
);

CREATE INDEX executions_session_turn
  ON executions(session_correlation, turn_number, created_at, execution_id);
CREATE INDEX executions_correlation_created
  ON executions(session_correlation, created_at DESC, execution_id DESC);
CREATE INDEX executions_canonical_created
  ON executions(canonical_session_id, created_at DESC, execution_id DESC);
CREATE INDEX executions_lifecycle_created
  ON executions(lifecycle, created_at, execution_id);
CREATE INDEX executions_runtime_context_turn
  ON executions(session_correlation, turn_number DESC, created_at DESC, execution_id DESC)
  WHERE lifecycle='settled' AND outcome='completed' AND adoption='non_canonical';
CREATE INDEX executions_parent_created
  ON executions(parent_execution_id, created_at);

CREATE TABLE execution_display_positions (
  session_correlation TEXT NOT NULL,
  execution_ordinal INTEGER NOT NULL,
  execution_id TEXT NOT NULL UNIQUE REFERENCES executions(execution_id) ON DELETE CASCADE,
  PRIMARY KEY(session_correlation, execution_ordinal)
);
CREATE INDEX execution_display_positions_execution
  ON execution_display_positions(execution_id);

CREATE TABLE messages (
  execution_id TEXT NOT NULL REFERENCES executions(execution_id) ON DELETE CASCADE,
  message_ordinal INTEGER NOT NULL,
  content_digest TEXT NOT NULL REFERENCES contents(content_digest),
  PRIMARY KEY(execution_id, message_ordinal)
);

CREATE TABLE session_turns (
  session_id TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
  turn_ordinal INTEGER NOT NULL,
  turn_number INTEGER NOT NULL,
  execution_id TEXT NOT NULL UNIQUE REFERENCES executions(execution_id),
  PRIMARY KEY(session_id, turn_ordinal),
  UNIQUE(session_id, turn_number)
);

CREATE TABLE conversation_messages (
  session_id TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
  message_ordinal INTEGER NOT NULL,
  turn_number INTEGER NOT NULL,
  execution_id TEXT NOT NULL,
  execution_message_ordinal INTEGER NOT NULL,
  PRIMARY KEY(session_id, message_ordinal),
  FOREIGN KEY(session_id, turn_number)
    REFERENCES session_turns(session_id, turn_number),
  FOREIGN KEY(execution_id, execution_message_ordinal)
    REFERENCES messages(execution_id, message_ordinal)
);
CREATE INDEX conversation_messages_turn
  ON conversation_messages(session_id, turn_number, message_ordinal);

CREATE TABLE session_model_changes (
  session_id TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
  change_ordinal INTEGER NOT NULL,
  effective_from_turn INTEGER NOT NULL,
  changed_at TEXT NOT NULL,
  selection_json TEXT NOT NULL,
  PRIMARY KEY(session_id, change_ordinal)
);

CREATE TABLE semantic_records (
  record_id TEXT PRIMARY KEY,
  execution_id TEXT NOT NULL REFERENCES executions(execution_id) ON DELETE CASCADE,
  ordinal INTEGER NOT NULL,
  kind TEXT NOT NULL,
  observed_at TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  content_digest TEXT REFERENCES contents(content_digest),
  UNIQUE(execution_id, ordinal)
);
CREATE INDEX semantic_records_execution_ordinal
  ON semantic_records(execution_id, ordinal);

CREATE TABLE semantic_relations (
  record_id TEXT NOT NULL REFERENCES semantic_records(record_id) ON DELETE CASCADE,
  relation_ordinal INTEGER NOT NULL,
  relation TEXT NOT NULL,
  target_record_id TEXT NOT NULL,
  mandatory INTEGER NOT NULL,
  resolved INTEGER NOT NULL,
  PRIMARY KEY(record_id, relation_ordinal)
);
CREATE INDEX semantic_relations_unresolved
  ON semantic_relations(resolved, target_record_id);

CREATE TABLE assistant_text_states (
  execution_id TEXT NOT NULL REFERENCES executions(execution_id) ON DELETE CASCADE,
  lane TEXT NOT NULL,
  model_step INTEGER NOT NULL,
  request_ordinal INTEGER NOT NULL,
  first_event_ordinal INTEGER NOT NULL,
  event_json TEXT NOT NULL,
  PRIMARY KEY(execution_id, lane, model_step, request_ordinal)
);

CREATE TABLE execution_contexts (
  execution_id TEXT PRIMARY KEY REFERENCES executions(execution_id) ON DELETE CASCADE,
  manifest_json TEXT NOT NULL
);

CREATE TABLE diagnostics (
  diagnostic_id TEXT PRIMARY KEY,
  execution_id TEXT NOT NULL REFERENCES executions(execution_id) ON DELETE CASCADE,
  content_digest TEXT NOT NULL REFERENCES contents(content_digest)
);

CREATE TABLE recall_relations (
  source_execution_id TEXT NOT NULL REFERENCES executions(execution_id) ON DELETE CASCADE,
  target_execution_id TEXT NOT NULL REFERENCES executions(execution_id),
  record_id TEXT NOT NULL UNIQUE REFERENCES semantic_records(record_id),
  PRIMARY KEY(source_execution_id, target_execution_id)
);

CREATE TABLE command_receipts (
  core_epoch TEXT NOT NULL,
  command_id TEXT NOT NULL,
  operation TEXT NOT NULL,
  signature_digest TEXT NOT NULL,
  result_json TEXT NOT NULL,
  target_json TEXT NOT NULL,
  execution_id TEXT,
  completed_at TEXT NOT NULL,
  PRIMARY KEY(core_epoch, command_id)
);

CREATE TABLE execution_control_facts (
  execution_id TEXT PRIMARY KEY REFERENCES executions(execution_id) ON DELETE CASCADE,
  control_json TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE follow_up_receipts (
  core_epoch TEXT NOT NULL,
  queue_id TEXT NOT NULL,
  session_correlation TEXT NOT NULL,
  command_id TEXT NOT NULL,
  parent_execution_id TEXT,
  text_content_digest TEXT NOT NULL REFERENCES contents(content_digest),
  status TEXT NOT NULL,
  reason TEXT,
  started_execution_id TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(core_epoch, queue_id)
);
CREATE INDEX follow_up_receipts_session_updated
  ON follow_up_receipts(core_epoch, session_correlation, updated_at DESC, queue_id DESC);

CREATE TABLE core_session_cursors (
  core_epoch TEXT NOT NULL,
  session_id TEXT NOT NULL,
  public_revision INTEGER NOT NULL,
  PRIMARY KEY(core_epoch, session_id)
);

CREATE TABLE data_session_read_cursors (
  data_instance_id TEXT NOT NULL,
  session_correlation TEXT NOT NULL,
  cut INTEGER NOT NULL,
  store_revision INTEGER NOT NULL,
  descriptor_sequence INTEGER NOT NULL,
  anchor_json TEXT NOT NULL,
  latest_execution_id TEXT REFERENCES executions(execution_id),
  PRIMARY KEY(data_instance_id, session_correlation)
);

INSERT INTO store_metadata(singleton, schema_version, created_at)
VALUES(1, 3, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
PRAGMA user_version = 3;
