export async function readFile(args, api) {
  return api.readFile(args);
}

export async function writeFile(args, api) {
  return api.writeFile({ path: args.path, text: args.text });
}

export async function editFile(args, api) {
  if (!Array.isArray(args.edits)) throw new Error("edit_file requires an edits array of {oldText, newText} replacements");
  return api.editFile(args);
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
