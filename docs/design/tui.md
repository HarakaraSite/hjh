# Terminal UI

I-05 separates `hjh-tui` from `hjh serve` over HTTP + JSON + SSE, as defined in
the [separation plan](../plans/i05-core-tui-separation.md) and [HTTP API](http-api.md).
The UI owns the draft, next Session/model/role selection, rendered transcript,
and client run handle. Core owns execution contexts, database, providers,
credentials, and JavaScript workers.

Bubble Tea's update loop is the only owner of visible state. Runs and history
queries execute in goroutines and send typed messages back to the loop. The UI
keeps one foreground run at a time while allowing the draft and read-only
history queries to remain available. Assistant text observations replace the
current cumulative text for their execution and model step. Snapshot and update
states replace entries by their IDs, including after reconnection. A later step gets its own entry,
and the final result updates that step instead of printing the same answer a
second time. Live progress displays operation names, phases, model steps, and
errors. Full observation payloads and model results remain available through
`/inspect`; they are not repeated as JSON in the live transcript.

The textarea accepts unbounded Japanese and multiline input. Enter submits;
Alt+Enter and Ctrl+J insert a newline. The transcript uses a soft-wrapping
viewport. Esc cancels a run and leaves the UI open. Ctrl+C and `/quit` cancel
an active run explicitly, wait for its terminal state, then exit. The client
retains its Core/run IDs when a start response is lost and recovers the same run.
Closing an HTTP request or SSE subscription only disconnects communication.
Normal UI exit cancels its foreground run and joins client jobs; Core remains
available. Core shutdown uses SIGINT/SIGTERM.

Commands are:

| Command | Behavior |
| --- | --- |
| `/sessions` | Open a Session picker; use Up/Down or j/k and Enter to resume the selected Session. |
| `/new` | Start a new conversation in the current workspace. A Session is created by its first run. |
| `/resume SESSION_ID` | Load the saved Session and adopted conversation. |
| `/history [FROM_TURN TO_TURN]` | Show adopted messages, optionally for an inclusive turn range. |
| `/executions` | List this Session's completed, failed, and cancelled executions. |
| `/inspect ID [FROM_ORDINAL TO_ORDINAL]` | Show execution messages, records, relations, captured configuration, and context. |
| `/roles` | Show the current workspace/config role catalog and tool lists. |
| `/role [NAME]` | Show the next-run role selection, or select a role for the next run. |
| `/providers` | List provider definitions from the current external config. |
| `/provider [ID]` | Select the next provider and clear previous model/effort/auth overrides, or show the selection. |
| `/models [ID]` | List the selected route's model candidates. |
| `/effort VALUE` | Select reasoning effort for the next run; auto uses the endpoint default. |
| `/auth [ID]` | Show ChatGPT login status without token values. |
| `/login ID` | Show the browser authorization URL. |
| `/login complete ID` | Complete an automatic local browser callback. |
| `/login callback ID URL` | Complete a browser callback pasted from another machine. |
| `/model MODEL_ID` | Select the model for the next run. A successful run records the selection through the shared Session adoption operation. |
| `/help` | Show command help. |
| `/quit` | Stop an active run, wait for it to settle, then exit. |

Session-changing commands received during a run are applied after that run
settles, so observations and results remain associated with the Session that
started them. Submitting ordinary text while a run is active leaves the draft
in the editor and reports that another run cannot start until the current one
ends.

Workspace paths refer to Core's filesystem. An omitted startup workspace uses
Core's default, rather than the TUI process's current directory.
The selected workspace is passed to each run as a start-time value. Resuming a
Session selects its saved workspace; `/new` then starts the next Session in
that selected workspace. Unless a model was explicitly supplied at startup or
selected with `/model`, the UI leaves model resolution to the shared backend's
configuration and Session rules. It shows the effective model reported by the
execution admission observation without replacing a model selected for a later
run.

## I-02: child executions

Delegation runs within the Core-owned foreground operation. Child observations display their
execution ID and parent ID as progress; child text does not replace the parent's
transcript, effective model, or current model step.
`/executions` shows parent and spawn IDs. `/inspect` shows the selected execution's
parent/spawn metadata and direct children, including when ordinal bounds restrict
the selected records. Inspect a child ID to read that child's evidence separately.

Esc stops the parent and its children and leaves the UI open. Ctrl+C and `/quit`
wait for child cancellation, worker closure, terminal persistence, and joining
before the terminal state is returned. The database stays open in Core after TUI exit.
The earlier same-process PTY verification is
recorded in the [I-02 results](../results/i02-child-agents.md).

## I-03: role selection

Startup --role and /role NAME set the explicit next-run selection. Otherwise the
backend resolves the saved Session role, then config.defaultRole. The execution
admission reports the actual role separately from the explicit selection for a
later run; /role changes during a run do not mutate that Worker. /roles reads
through the scoped HTTP catalog operation. /new retains the selected workspace;
/resume restores saved workspace and role unless an explicit role is selected.
Child progress includes its own role without changing the parent's role/model/step.
Execution listings and inspection display saved role names.

## I-04: connection selection

Provider, model, effort, and credential reference are captured when submitting
an execution. Selection commands during a run affect the next run. The header
shows the running root's actual provider/model/effort separately from the next
selection. Child progress cannot replace it. Session resume uses the saved
selection unless explicitly overridden; provider changes use the new route's
defaults. JSON and OAuth ownership are described in [providers](providers.md).
Core owns pending OAuth callbacks and their captured workspace/provider/profile.
Showing a login URL does not change the next-run provider selection.
