# write_file

Create or replace an entire UTF-8 text file with {path,text}; missing parent
directories are created. Paths are workspace-relative or absolute. Use edit_file
for literal replacements when you do not intend to replace the whole file.
The result contains the supplied path and UTF-8 bytes written, not character count.
The JavaScript tool awaits stdlib fs operations before normal completion. On error
or cancellation, a started OS write may still finish after VM closure; inspect the
actual file before retrying. Closing a VM does not roll back file changes.

This run_js example requires write_file and read_file selected. It creates a
uniquely named temporary example file, reads it back, and leaves it in place.

```js
// Write a temporary note and read the saved text.
const path = "/tmp/hjh-help-write-" + Date.now() + ".txt";
const written = await tools.write_file({path,text:"example note\n"});
const read = await tools.read_file({path});
return {written,text:read.text};
```
