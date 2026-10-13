export async function readFile(args, api) {
  return api.readFile(args);
}

export async function writeFile(args, api) {
  return api.writeFile({ path: args.path, text: args.text });
}

export async function editFile(args, api) {
  if (!Array.isArray(args.edits)) throw new Error("edit_file requires an edits array of {oldText, newText} replacements");
  const original = await api.readFile({ path: args.path });
  let text = original.text;
  const edits = [];
  let replacements = 0;
  for (const edit of args.edits) {
    const count = edit.oldText === "" ? 0 : text.split(edit.oldText).length - 1;
    if (count > 0) text = text.split(edit.oldText).join(edit.newText);
    edits.push({ replacements: count });
    replacements += count;
  }
  const saved = await api.writeFile({ path: args.path, text });
  return { path: args.path, replacements, edits, bytes: saved.bytes };
}

export async function command(args, api) {
  return api.command({ program: args.program, args: args.args ?? [], stdin: args.stdin ?? "" });
}

export async function http(args, api) {
  return api.http({ url: args.url });
}

export async function webFetch(args, api) {
  const input = { url: args.url };
  if (args.save_to !== undefined) input.save_to = args.save_to;
  return api.webFetch(input);
}
