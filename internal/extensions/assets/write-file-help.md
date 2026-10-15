# write_file

Create or replace an entire UTF-8 text file with {path,text}; missing parent
directories are created. Paths are workspace-relative or absolute. Use edit_file
for literal replacements when you do not intend to replace the whole file.
The result contains path and bytes written.

This run_js example requires write_file and read_file selected. It creates a
uniquely named temporary example file, reads it back, and leaves it in place.

```js
// Write a temporary note and read the saved text.
const path = "/tmp/hjh-help-write-" + Date.now() + ".txt";
const written = await tools.write_file({path,text:"example note\n"});
const read = await tools.read_file({path});
return {written,text:read.text};
```
