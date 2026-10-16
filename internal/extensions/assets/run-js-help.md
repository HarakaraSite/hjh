# run_js

Choose generated JavaScript for composition, independent parallel calls, aggregation,
and string/JSON processing. A single read or edit usually needs only its dedicated tool.
Send `{code:"..."}`: code is an async function body, not a complete program or module.
Start with a short English `//` purpose comment, await operations, and return the data
needed in the next model input. Unreturned local values are not automatically shown.

`tools.NAME(args)` calls a selected registered tool through its normal invocation path.
Only selected tools exist in that dictionary. Examples do not add tools to the current role. `api.tools`
is the same selected-tool interface. These examples require run_js and every tool
named in their code. The go-quickjs stdlib is also available independently of the
selected tools: timers, fetch, Buffer, TextEncoder/TextDecoder and built-in modules
such as `node:fs`, `node:path` and `node:child_process`. Use `await import("node:fs")`
inside this async body; static import declarations are not valid here. This is not
a complete Node.js environment; npm downloads and node_modules resolution are not
provided. Do not assume arbitrary Node packages or native addons work.

Await required operations and return their results. Normal calls process started
I/O, callbacks and timers before completing; they do not run in the background
across calls. Clear intervals when finished, preferably in finally. For a delay use
`await new Promise(resolve => setTimeout(resolve, 1000))`. An error caught inside JS
follows this normal path. An uncaught error reaching the outer call closes that VM;
the next call loads the same captured source into a fresh VM. Global/module state
is reset and module initialization reruns; the failed call is not retried. Successful
calls retain VM/module state within this execution, not a later Session resume.
On error or cancellation, already-started async filesystem operations may still finish
after VM closure; closing the VM drops JS delivery, not OS side effects. Await file
operations required by the task and inspect the actual file when recovering.

For raw filesystem operations, pass absolute paths; use
`path.resolve(process.cwd(), relativePath)` to construct a workspace-based path.
`process.cwd()` and default child-process cwd are the execution workspace; raw fs
relative paths use the Core process cwd, not that workspace. Prefer selected file
tools for their line ranges, paging and editing.

`child_process` cancellation stops only the directly started process. If that program
starts descendants, see command help for examples and explicit process inspection/kill.
Direct stdlib I/O is not individually logged like a registered tool/hook call.

Call `tools.read_file({help:true})` (or another selected tool) to get
`{name,available,text}` without running its normal operation; return that result when
the model needs it. Help needs no normal required arguments. `available:false` means
no registered detail, not that the tool cannot be used. Third-party tools may omit
helpFile. Help comes from this execution's captured config/resources; after a new
turn or resume, re-query details if the current contract is unclear.

## Built-in modules and timers

Requires run_js only. This leaves a uniquely named example file in the workspace.
The explicit absolute path keeps raw fs independent of the Core process cwd.

```js
// Write and read a workspace file with built-in modules and an awaited timer.
const fs = await import("node:fs");
const path = await import("node:path");
const file = path.resolve(process.cwd(), "hjh-stdlib-example-" + Date.now() + ".txt");
await fs.promises.writeFile(file, "stdlib example\n");
await new Promise(resolve => setTimeout(resolve, 5));
return {path:file, text:await fs.promises.readFile(file, "utf8")};
```

## Independent reads in parallel

Requires read_file. Run from this repository, or replace paths with two known files
in the task workspace. Retain paths, line offsets, and continuation positions.

```js
// Read independent documents and retain exact source references.
const paths = ["docs/design/extensions.md", "docs/design/base-instructions.md"];
const results = await Promise.all(paths.map(path => tools.read_file({path, offset:1, limit:40})));
return results.map(r => ({path:r.path, offset:r.offset, text:r.text, nextOffset:r.nextOffset}));
```

Only independent work belongs in Promise.all. Multiple ordinary tool calls in a
model response execute sequentially; merely listing them together is not parallelism.

## Read JSON and aggregate it

Requires write_file and read_file. This self-contained example creates a uniquely
named temporary file, parses read_file's text, and returns totals plus record IDs.
It leaves that example file in place; it does not change task files.

```js
// Aggregate JSON records while retaining their source and identifiers.
const path = "/tmp/hjh-help-json-" + Date.now() + ".json";
await tools.write_file({path, text:JSON.stringify([{id:"a",count:2},{id:"b",count:3}])});
const source = await tools.read_file({path});
const records = JSON.parse(source.text);
return {path:source.path, ids:records.map(r => r.id), total:records.reduce((sum,r) => sum + r.count,0)};
```

## Dependent read, edit, and verify

Requires write_file, read_file, and edit_file. The edit is built from the awaited
read, and verification follows the edit; these operations are not independent.
The uniquely named temporary example file is left in place.

```js
// Read a temporary file, derive its replacement, and verify the saved edit.
const path = "/tmp/hjh-help-edit-" + Date.now() + ".txt";
await tools.write_file({path, text:"status: draft\n"});
const before = await tools.read_file({path});
const edit = await tools.edit_file({path, edits:[{oldText:before.text,newText:before.text.replace("draft","ready")}]});
const after = await tools.read_file({path});
return {path, edits:edit.edits, replacements:edit.replacements, text:after.text};
```

Zero replacements is a normal edit result, not proof that the intended change was
made. Inspect per-edit counts and the relevant saved text.

## Child operations

When the corresponding tool is selected and the runtime has the child runner or
controller supplied by the app, generated code also has these aliases:

| Alias | Selected tool |
| --- | --- |
| api.agents.run | run_agent |
| api.agents.spawn | spawn_agent |
| api.agents.status | agent_status |
| api.agents.steer | steer_agent |
| api.agents.collect | collect_agent |
| api.agents.cancel | cancel_agent |

These are selected-tool aliases, not an unrestricted host API. Call any selected
child tool with `{help:true}` for the shared child help, including executable
spawn → parent read → optional status/steer → collect and parallel blocking-run
examples. They can also be called directly without run_js. Parent conversation is
not copied to children. Collect needed results before finishing the parent because
remaining children are cancelled when the parent ends.
