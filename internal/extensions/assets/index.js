export async function readFile(args, api) {
  return api.readFile({ path: args.path });
}

export async function writeFile(args, api) {
  return api.writeFile({ path: args.path, text: args.text });
}

export async function editFile(args, api) {
  const original = await api.readFile({ path: args.path });
  const replacements = args.oldText === "" ? 0 : original.text.split(args.oldText).length - 1;
  const text = args.oldText === "" ? original.text : original.text.split(args.oldText).join(args.newText);
  const saved = await api.writeFile({ path: args.path, text });
  return { path: args.path, replacements, bytes: saved.bytes };
}

export async function command(args, api) {
  return api.command({ program: args.program, args: args.args ?? [], stdin: args.stdin ?? "" });
}

export async function http(args, api) {
  return api.http({ url: args.url });
}
