# command

Choose command for necessary external operations that JavaScript composition and
the selected dedicated tools do not accomplish: for example builds, tests, or Git
commit/push when requested. Wrapping a needless shell operation in run_js does not
make it useful composition. Use dedicated file, search, and Git inspection tools
for their operations; command remains available when an external program is needed.

Pass an executable name/path in program and individual strings in args. Working
directory is the workspace. args defaults to [], stdin to an empty string. This
waits for process exit and returns {exitCode,stdout,stderr}. A nonzero exit is a
normal result, so check exitCode and stderr. Failure to start a program is a tool
error, not a successful exit result. Output captures stdout and stderr up to 32 MiB
each; later bytes are discarded. Shell pipes, redirects, variable
expansion, and && work only when you explicitly invoke a shell.

For example, directly call command({program:"go",args:["test","./internal/extensions"]})
when testing that package is the needed operation. An explicit shell example,
requiring run_js and command selected and sh available:

```js
// Run an explicit shell pipeline and preserve its exit status and output.
return await tools.command({program:"sh",args:["-c","printf hello | wc -c"]});
```

## Subprocesses and cancellation

Both command and direct use of go-quickjs `child_process` stop only the process
they directly started.
If that program starts descendants (for example sh → sleep, npm → node, or make →
compiler), they can remain after cancellation. A descendant holding stdout/stderr
open can also delay output collection. This concerns processes, not timers, fetch,
or child agents. Raw `child_process` captures at most 32 MiB each of stdout and
stderr; later output is discarded. Its `execFile` Promise rejects on nonzero exit
and carries code/stdout/stderr on the error, unlike the command tool result above.

If a descendant needs to be stopped, inspect it with command, for example
`{program:"ps",args:["-eo","pid,ppid,pgid,args"]}`. Identify the process belonging to
the task, then use `{program:"kill",args:["<pid>"]}`. If it does not stop and forced
termination is needed, use `{program:"kill",args:["-KILL","<pid>"]}`; inspect again.
Stdlib does not automatically clean up descendants.

The tool implementation uses stdlib child_process and records the tool call/result,
not a separate Host event. Omitted or empty stdin delivers immediate EOF. The
ordinary configured environment is available except configured API credential
variables; credentials are resolved only by the authenticated Go request bridge.
