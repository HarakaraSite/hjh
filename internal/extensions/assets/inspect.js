function page(items, args, defaultLimit = 100) {
  const offset = args.offset ?? 0;
  const limit = args.limit ?? defaultLimit;
  if (!Number.isInteger(offset) || offset < 0 || !Number.isInteger(limit) || limit < 1) {
    throw new Error("offset must be nonnegative and limit must be positive");
  }
  const selected = items.slice(offset, offset + limit);
  const next = offset + selected.length;
  return { items: selected, offset, total: items.length,
    ...(next < items.length ? { nextOffset: next } : {}) };
}

export async function listDirectory(args, api) {
  return api.listDirectory(args);
}

export async function findFiles(args, api) {
  return api.findFiles(args);
}

export async function countFiles(args, api) {
  return api.countFiles(args);
}

let grepSelection;
async function selectGrepBackend(api) {
  const rg = await api.resolveExecutable({ program: "rg" });
  if (rg) return { backend: "rg", program: rg };
  const grep = await api.resolveExecutable({ program: "grep" });
  if (grep) return { backend: "grep", program: grep };
  throw new Error("grep requires rg or GNU grep on PATH");
}

// --json retains names containing newlines and records matching/context lines.
async function ripgrep(args, api, program) {
  const argv = ["--json", "--hidden", "--color", "never"];
  if (args.patternKind === "literal") argv.push("--fixed-strings");
  if (args.caseSensitive === false) argv.push("--ignore-case");
  if (args.includeIgnored === true) argv.push("--no-ignore");
  if (args.context !== undefined) argv.push("--context", String(args.context));
  for (const glob of args.glob ?? []) argv.push("--glob", glob);
  argv.push("--regexp", args.pattern, "--", args.path ?? ".");
  const result = await api.command({ program, args: argv });
  if (result.exitCode > 1) throw new Error(`grep failed (${result.exitCode}): ${result.stderr.trim()}`);
  const lines = [];
  const files = new Set();
  let matches = 0;
  for (const raw of result.stdout.split("\n")) {
    if (!raw) continue;
    const event = JSON.parse(raw);
    if (event.type !== "match" && event.type !== "context") continue;
    const data = event.data;
    // rg emits bytes for paths/text that are not UTF-8. Keep those losslessly.
    const path = data.path.text ?? { base64: data.path.bytes };
    const text = data.lines.text ?? { base64: data.lines.bytes };
    const matched = event.type === "match";
    if (matched) {
      matches++;
      files.add(JSON.stringify(path));
    }
    lines.push({ path, line: data.line_number, text, match: matched });
  }
  return { lines, files: [...files].map(value => JSON.parse(value)), matches,
    ...(result.stderr ? { warnings: result.stderr } : {}) };
}

async function gnuGrep(args, api, program) {
  const argv = ["--recursive", "--with-filename", "--line-number", "--null",
    "--color=never", "--no-group-separator", "--binary-files=without-match"];
  argv.push(args.patternKind === "literal" ? "--fixed-strings" : "--extended-regexp");
  if (args.caseSensitive === false) argv.push("--ignore-case");
  if (args.context !== undefined) argv.push("--context", String(args.context));
  for (const glob of args.glob ?? []) {
    if (glob.startsWith("!")) argv.push(`--exclude=${glob.slice(1)}`);
    else argv.push(`--include=${glob}`);
  }
  argv.push("--regexp", args.pattern, "--", args.path ?? ".");
  return api.grepCommand({ program, args: argv });
}

export async function grep(args, api) {
  const selectedBackend = await (grepSelection ??= selectGrepBackend(api));
  const result = await (selectedBackend.backend === "rg"
    ? ripgrep(args, api, selectedBackend.program) : gnuGrep(args, api, selectedBackend.program));
  const output = args.output ?? "lines";
  if (output !== "lines" && output !== "files") throw new Error("grep output must be lines or files");
  const values = output === "files" ? result.files : result.lines;
  const selected = page(values, args);
  return { path: args.path ?? ".", output, matches: result.matches,
    backend: selectedBackend.backend,
    ignoreApplied: selectedBackend.backend === "rg" && args.includeIgnored !== true,
    ...(output === "files" ? { files: selected.items } : { lines: selected.items }),
    offset: selected.offset, total: selected.total,
    ...(selected.nextOffset !== undefined ? { nextOffset: selected.nextOffset } : {}),
    ...(result.warnings ? { warnings: result.warnings } : {}) };
}

export async function gitInspect(args, api) {
  const op = args.op;
  const argv = ["--no-pager", "--no-optional-locks", op];
  if (op === "status") {
    argv.push("--porcelain=v1", "--untracked-files=all");
  } else if (op === "diff" || op === "show") {
    argv.push("--no-ext-diff", "--no-textconv", "--color=never");
    if (op === "diff" && args.staged) argv.push("--cached");
    if (args.stat) argv.push("--stat");
    if (!args.stat && args.context !== undefined) argv.push(`--unified=${args.context}`);
    if (args.rev !== undefined) argv.push("--end-of-options", args.rev);
    else if (op === "show") argv.push("HEAD");
  } else if (op === "log") {
    // Offset/limit select commits, without generating the whole history.
    const offset = args.offset ?? 0;
    const limit = args.limit ?? 20;
    page([], { offset, limit });
    argv.push("--oneline", "--color=never", `--skip=${offset}`, `--max-count=${limit + 1}`);
    if (args.rev !== undefined) argv.push("--end-of-options", args.rev);
  } else {
    throw new Error("git_inspect op must be status, diff, log, or show");
  }
  argv.push("--", ...(args.paths ?? []));
  const result = await api.command({ program: "git", args: argv });
  if (result.exitCode !== 0) throw new Error(`git_inspect ${op} failed (${result.exitCode}): ${result.stderr.trim()}`);
  const lines = result.stdout === "" ? [] : result.stdout.replace(/\n$/, "").split("\n");
  if (op === "log") {
    const offset = args.offset ?? 0;
    const limit = args.limit ?? 20;
    return { op, text: lines.slice(0, limit).join("\n"), offset,
      ...(lines.length > limit ? { nextOffset: offset + limit } : {}) };
  }
  const selected = page(lines, args);
  return { op, text: selected.items.join("\n"), offset: selected.offset, total: selected.total,
    ...(selected.nextOffset !== undefined ? { nextOffset: selected.nextOffset } : {}) };
}
