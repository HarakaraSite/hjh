# command

Choose command for necessary external operations that JavaScript composition and
the selected dedicated tools do not accomplish: for example builds, tests, or Git
commit/push when requested. Wrapping a needless shell operation in run_js does not
make it useful composition. Use dedicated file, search, and Git inspection tools
for their operations; command remains available when an external program is needed.

Pass an executable name/path in program and individual strings in args. Working
directory is the workspace. args defaults to [], stdin to an empty string. This
waits for process exit and returns {exitCode,stdout,stderr}. A nonzero exit is a
normal result, so check exitCode and stderr. Shell pipes, redirects, variable
expansion, and && work only when you explicitly invoke a shell.

For example, directly call command({program:"go",args:["test","./internal/extensions"]})
when testing that package is the needed operation. An explicit shell example,
requiring run_js and command selected and sh available:

```js
// Run an explicit shell pipeline and preserve its exit status and output.
return await tools.command({program:"sh",args:["-c","printf hello | wc -c"]});
```
