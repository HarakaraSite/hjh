# Workspace inspection: ls, find, grep, wc, git_inspect

Use ls for structure, find for paths, grep for file contents, wc for counts, and
git_inspect for Git evidence. They are read-only operations; use the tool matching
the task rather than a shell substitute. Paths are workspace-relative or absolute.
Each example below is an async run_js body and needs run_js plus every named tool
selected; direct single-tool calls work without run_js.

## ls and find

ls({path?,tree?,depth?,offset?,limit?}) returns a flat entries array with path, type,
bytes, modifiedAt, and depth. tree defaults false; depth defaults 3, direct children
are depth 1. Dot entries are included; directory symlinks are listed, not traversed.

find({path?,pattern?,type?,exclude?,offset?,limit?}) returns entries with path and
type. A slashless glob matches basenames at any depth; ** spans directories.
Hidden/ignored entries are included. exclude globs can skip directory subtrees.
Defaults are workspace path, pattern *, type any, no exclusions.

Both use zero-based offset, default limit 100, and returned nextOffset to continue.
This repository example finds Go files before counting them (first page only).
Use nextOffset for more discovered paths when needed.

```js
// Find source paths before counting their contents.
const found = await tools.find({path:"internal/extensions",pattern:"*.go",type:"file",limit:20});
const paths = found.entries.map(entry => entry.path);
const counts = paths.length ? await tools.wc({paths}) : null;
return {entries:found.entries,nextOffset:found.nextOffset,counts};
```

## grep

grep({pattern,path?,...}) prefers rg, otherwise GNU grep. patternKind:"literal"
selects exact text; default regex uses the selected backend's syntax. Filter by
path/glob before requesting a small context/limit. No matches is a normal empty
result. lines output returns path, 1-based line, text, and match (false for context);
output:"files" returns unique files. offset/limit page all returned lines including
context. Continue with nextOffset and unchanged search options.

The result includes backend and ignoreApplied: rg respects ignore rules unless
includeIgnored:true; GNU grep does not apply .gitignore. Both include hidden files.
GNU grep uses extended regex, basename include/exclude globs, and skips binary
files; rg supports ordered path globs with ! exclusions. Do not assume the two
backends have identical regex/glob behavior.

```js
// Find relevant source lines while retaining the evidence needed for a follow-up read.
const result = await tools.grep({path:"internal/extensions",pattern:"CaptureOptions",patternKind:"literal",glob:"*.go",context:2,limit:30});
return {backend:result.backend,ignoreApplied:result.ignoreApplied,lines:result.lines,nextOffset:result.nextOffset};
```

Use the returned path/line to choose a read_file range; reading that file depends
on discovery and should follow it, not run in parallel with it.

## wc

wc({paths:[...]}) returns per-file counts in files and combined counts in total.
lines counts newline characters (like wc -l), not display lines: a final line
without a newline does not add one. words splits Unicode whitespace; bytes measures
file bytes. Use find first when the paths are not already known.

## git_inspect

git_inspect supports op:"status"|"diff"|"log"|"show", not arbitrary Git commands.
status returns porcelain text; diff defaults to unstaged changes, staged:true uses
the index. paths limits Git pathspecs; rev selects a revision (show defaults HEAD).
stat requests diff/show statistics instead of patches; context selects patch context.
It returns text and continuation position without updating index or working tree.
For log, offset/limit count commits (default 20); other operations page output lines
(default 100). Both use zero-based offset and returned nextOffset.

```js
// Read independent Git evidence for the assigned source scope.
const [status,diff] = await Promise.all([
  tools.git_inspect({op:"status",paths:"internal/extensions"}),
  tools.git_inspect({op:"diff",paths:"internal/extensions",stat:true})
]);
return {status,diff};
```
