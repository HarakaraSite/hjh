# edit_file

Use {path,edits:[{oldText,newText},...]} even for one replacement. Each edit
replaces every exact literal match in the text produced by preceding edits, then
the file is saved once. This is not regex or patch syntax. Empty oldText makes no
replacement; empty newText deletes matches. A missing match returns 0, not an error.
Inspect edits[].replacements and total replacements before claiming success.

This run_js body requires write_file, read_file, and edit_file selected. It creates
a uniquely named temporary file and leaves it in place. Read → derive edit → read
is dependent work, not a Promise.all operation.

```js
// Derive a replacement from a temporary file and verify the saved result.
const path = "/tmp/hjh-help-literal-edit-" + Date.now() + ".txt";
await tools.write_file({path,text:"draft draft\n"});
const before = await tools.read_file({path});
const edited = await tools.edit_file({path,edits:[{oldText:before.text,newText:before.text.replace(/draft/g,"ready")}]});
const after = await tools.read_file({path});
return {path,edits:edited.edits,replacements:edited.replacements,text:after.text};
```
