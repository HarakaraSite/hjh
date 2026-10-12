package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/app"
	"hjh/internal/contracts"
	"hjh/internal/conversation"
	"hjh/internal/history"
	"hjh/internal/historyview"
	"hjh/internal/sessionid"
)

type historyView string

const (
	historyViewSession   historyView = "session"
	historyViewCanonical historyView = "canonical"
	historyViewDetail    historyView = "detail"
)

type historyCommand struct {
	workspace       string
	workspaceGiven  bool
	dbPath          string
	connect         string
	sessionSelector string
	latest          bool
	view            historyView
	fromTurn        int64
	toTurn          int64
	json            bool
}

type historyReader struct {
	local  *app.Service
	remote *apiclient.Client
}

func (r historyReader) sessions(ctx context.Context) ([]history.Session, error) {
	if r.remote == nil {
		return r.local.Sessions(ctx)
	}
	sessions, err := r.remote.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]history.Session, len(sessions))
	for i, session := range sessions {
		result[i] = historyview.SessionFromAPI(session)
	}
	return result, nil
}

func (r historyReader) sessionView(ctx context.Context, id string) (api.ConversationSnapshot, error) {
	if r.remote != nil {
		return r.remote.SessionView(ctx, id)
	}
	cut, err := r.local.CaptureSessionConversationCut(ctx, id)
	if err != nil {
		return api.ConversationSnapshot{}, err
	}
	view := conversation.NewSessionView(id)
	defer view.Release()
	roots := make([]api.ConversationCut, len(cut.Roots))
	for i, root := range cut.Roots {
		through := copyHistorySequence(root.ThroughSequence)
		head := make([]api.ConversationExecutionHead, len(root.Executions))
		for j, execution := range root.Executions {
			head[j] = api.ConversationExecutionHead{
				ExecutionID: execution.ExecutionID, ParentExecutionID: execution.ParentExecutionID,
				SemanticOrdinal: execution.LatestRecordOrdinal, MessageOrdinal: execution.LatestMessageOrdinal,
			}
		}
		roots[i] = api.ConversationCut{RootExecutionID: root.RootExecutionID,
			RootExecutionOrdinal: root.RootExecutionOrdinal, ThroughSequence: through, Executions: head}
	}
	if err := view.LoadCut(ctx, nil, roots, func(visit func(contracts.ExecutionFact) error) error {
		return r.local.WalkSessionConversationCut(ctx, cut, visit)
	}); err != nil {
		return api.ConversationSnapshot{}, err
	}
	return view.Snapshot(ctx)
}

func (r historyReader) conversation(ctx context.Context, id string, from, to int64) ([]history.Message, error) {
	if r.remote == nil {
		return r.local.Conversation(ctx, id, from, to)
	}
	messages, err := r.remote.Conversation(ctx, id, from, to)
	if err != nil {
		return nil, err
	}
	result := make([]history.Message, len(messages))
	for i, message := range messages {
		result[i] = history.Message{ExecutionID: message.ExecutionID, Ordinal: message.Ordinal,
			TurnNumber: message.TurnNumber, JSON: append(json.RawMessage(nil), message.JSON...)}
	}
	return result, nil
}

func (r historyReader) executions(ctx context.Context, id string) ([]history.Execution, error) {
	if r.remote == nil {
		return r.local.Executions(ctx, id)
	}
	executions, err := r.remote.Executions(ctx, id)
	if err != nil {
		return nil, err
	}
	result := make([]history.Execution, len(executions))
	for i, execution := range executions {
		result[i] = historyview.ExecutionFromAPI(execution)
	}
	return result, nil
}

func (r historyReader) inspect(ctx context.Context, id string) (historyview.Inspection, error) {
	if r.remote == nil {
		inspection, err := r.local.Inspect(ctx, id, 0, -1)
		if err != nil {
			return historyview.Inspection{}, err
		}
		return historyview.Inspection{Execution: inspection.Execution, Children: inspection.Children,
			Messages: inspection.Messages, Records: inspection.Records,
			Configuration: inspection.Configuration, Context: inspection.Context}, nil
	}
	inspection, err := r.remote.Inspect(ctx, id, 0, -1)
	if err != nil {
		return historyview.Inspection{}, err
	}
	return historyview.InspectionFromAPI(inspection), nil
}

func copyHistorySequence(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// runHistory reads local history without creating a database, or queries the
// existing Core API when --connect is supplied.
func runHistory(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	command, err := parseHistoryCommand(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, err)
		return 2
	}
	if command.connect != "" {
		return runRemoteHistory(ctx, command, stdout, stderr)
	}
	return runLocalHistory(ctx, command, stdout, stderr)
}

func parseHistoryCommand(args []string, stderr io.Writer) (historyCommand, error) {
	var command historyCommand
	f := flag.NewFlagSet("history", flag.ContinueOnError)
	f.SetOutput(stderr)
	workspace := f.String("workspace", "", "workspace to select latest history from")
	dbPath := f.String("db", "", "history database (default: WORKSPACE/.hjh/history.sqlite3)")
	connect := f.String("connect", "", "read history from an existing Core URL")
	session := f.String("session", "", "Session ID or unique case-insensitive prefix (8 characters by default)")
	latest := f.Bool("latest", false, "select the latest Session")
	view := f.String("view", string(historyViewSession), "history view: session, canonical or detail")
	fromTurn := f.Int64("from-turn", 0, "first turn to include (inclusive)")
	toTurn := f.Int64("to-turn", -1, "last turn to include (inclusive)")
	jsonOutput := f.Bool("json", false, "write adopted messages as JSON")
	if err := f.Parse(args); err != nil {
		return command, err
	}
	if f.NArg() > 0 {
		return command, fmt.Errorf("history does not accept positional arguments: %s", strings.Join(f.Args(), " "))
	}
	if *session != "" && *latest {
		return command, fmt.Errorf("--session and --latest are mutually exclusive")
	}
	if *fromTurn < 0 {
		return command, fmt.Errorf("--from-turn must be zero or greater")
	}
	if *view != string(historyViewSession) && *view != string(historyViewCanonical) && *view != string(historyViewDetail) {
		return command, fmt.Errorf("--view must be session, canonical or detail")
	}
	if *connect != "" {
		if *dbPath != "" {
			return command, fmt.Errorf("--db cannot be used with --connect")
		}
	}
	command.workspace = *workspace
	command.workspaceGiven = *workspace != ""
	command.dbPath = *dbPath
	command.connect = *connect
	command.sessionSelector = strings.TrimSpace(*session)
	command.latest = *latest
	command.view = historyView(*view)
	command.fromTurn = *fromTurn
	command.toTurn = *toTurn
	command.json = *jsonOutput
	return command, nil
}

func runLocalHistory(ctx context.Context, command historyCommand, stdout, stderr io.Writer) int {
	workspace := command.workspace
	if workspace == "" {
		var err error
		workspace, err = os.Getwd()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	dbPath := command.dbPath
	if dbPath == "" {
		dbPath = filepath.Join(workspace, ".hjh", "history.sqlite3")
	}
	dbPath, err = filepath.Abs(dbPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(stderr, "# no history")
		return 0
	} else if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	store, err := history.OpenReadOnly(dbPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer store.Close()
	service := app.NewService(store, nil, app.RunOptions{Workspace: workspace})
	defer service.Close()
	return renderHistory(ctx, historyReader{local: service}, workspace, command, stdout, stderr)
}

func runRemoteHistory(ctx context.Context, command historyCommand, stdout, stderr io.Writer) int {
	client, err := apiclient.New(command.connect)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer client.Close()
	core, err := client.Core(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	workspace := core.Workspace
	if command.workspaceGiven {
		if filepath.IsAbs(command.workspace) {
			workspace = command.workspace
		} else {
			workspace = filepath.Join(core.Workspace, command.workspace)
		}
	}
	if workspace != "" {
		workspace, err = filepath.Abs(workspace)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	return renderHistory(ctx, historyReader{remote: client}, workspace, command, stdout, stderr)
}

func renderHistory(ctx context.Context, reader historyReader, workspace string, command historyCommand, stdout, stderr io.Writer) int {
	sessions, err := reader.sessions(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	session, ok, err := historyview.SelectSession(sessions, workspace, command.sessionSelector)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !ok {
		fmt.Fprintln(stderr, "# no history")
		return 0
	}
	knownSessionIDs := historySessionIDs(sessions)
	fmt.Fprintf(stderr, "# session %s\n", sessionid.Short(session.ID, knownSessionIDs...))
	if command.json {
		messages, err := reader.conversation(ctx, session.ID, command.fromTurn, command.toTurn)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		apiMessages := make([]api.Message, len(messages))
		for i, message := range messages {
			apiMessages[i] = api.Message{ExecutionID: message.ExecutionID, Ordinal: message.Ordinal,
				TurnNumber: message.TurnNumber, JSON: append(json.RawMessage(nil), message.JSON...)}
		}
		if err := writeHistoryJSON(stdout, apiMessages); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	switch command.view {
	case historyViewSession:
		snapshot, err := reader.sessionView(ctx, session.ID)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		executions, err := reader.executions(ctx, session.ID)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		text := historyview.RenderSession(snapshot, executions, command.fromTurn, command.toTurn)
		if _, err := io.WriteString(stdout, text); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	case historyViewCanonical:
		messages, err := reader.conversation(ctx, session.ID, command.fromTurn, command.toTurn)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		text := historyview.RenderCanonical(session, messages, knownSessionIDs...)
		if _, err := io.WriteString(stdout, text); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	case historyViewDetail:
		if err := renderDetailHistory(ctx, reader, session, command, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	default:
		fmt.Fprintf(stderr, "unsupported history view %q\n", command.view)
		return 2
	}
	return 0
}

func writeHistoryJSON(stdout io.Writer, value any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func renderDetailHistory(ctx context.Context, reader historyReader, session history.Session, command historyCommand, stdout io.Writer) error {
	snapshot, err := reader.sessionView(ctx, session.ID)
	if err != nil {
		return err
	}
	executions, err := reader.executions(ctx, session.ID)
	if err != nil {
		return err
	}
	if err := historyview.WriteDetailSession(stdout, session); err != nil {
		return err
	}
	for _, execution := range executions {
		if !historyview.InTurnRange(execution.Turn, command.fromTurn, command.toTurn) {
			continue
		}
		inspection, err := reader.inspect(ctx, execution.ID)
		if err != nil {
			return err
		}
		if err := historyview.WriteDetailInspection(stdout, inspection); err != nil {
			return err
		}
	}
	return historyview.WriteDetailLiveEntities(stdout, snapshot, executions, command.fromTurn, command.toTurn)
}
