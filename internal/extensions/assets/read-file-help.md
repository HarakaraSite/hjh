# read_file

Read UTF-8 text using {path,offset?,limit?}. Paths are workspace-relative or absolute.
offset is a 1-based line number (default 1); omit limit to read through EOF. Use a
range when only a section matters. The result contains path, text, offset, lines,
totalLines, and nextOffset when more lines remain. Process result.text, not the
whole result object; continue using the returned nextOffset.

A single read needs no run_js: read_file({path:"docs/design/extensions.md",offset:1,limit:40}).
For independent reads plus aggregation, use the following run_js body. It requires
run_js and read_file selected and these repository files (substitute known paths).


```js
// Read independent source ranges and preserve their locations.
const paths = ["docs/design/extensions.md","docs/design/base-instructions.md"];
const reads = await Promise.all(paths.map(path => tools.read_file({path,offset:1,limit:40})));
return reads.map(r => ({path:r.path,offset:r.offset,text:r.text,nextOffset:r.nextOffset}));
```

Locate unknown paths with find, or relevant text with grep, before selecting a
read range. Preserve path and offset alongside excerpts so results remain citable.
