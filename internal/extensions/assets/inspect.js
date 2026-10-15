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
  for (const glob of typeof args.glob === "string" ? [args.glob] : args.glob ?? []) argv.push("--glob", glob);
  argv.push("--regexp", args.pattern, "--", args.path ?? ".");
  return api.grepCommand({ program, args: argv, backend: "rg",
    output: args.output, offset: args.offset, limit: args.limit });
}

async function gnuGrep(args, api, program) {
  const argv = ["--recursive", "--with-filename", "--line-number", "--null",
    "--color=never", "--no-group-separator", "--binary-files=without-match"];
  argv.push(args.patternKind === "literal" ? "--fixed-strings" : "--extended-regexp");
  if (args.caseSensitive === false) argv.push("--ignore-case");
  if (args.context !== undefined) argv.push("--context", String(args.context));
  for (const glob of typeof args.glob === "string" ? [args.glob] : args.glob ?? []) {
    if (glob.startsWith("!")) argv.push(`--exclude=${glob.slice(1)}`);
    else argv.push(`--include=${glob}`);
  }
  argv.push("--regexp", args.pattern, "--", args.path ?? ".");
  return api.grepCommand({ program, args: argv, backend: "grep",
    output: args.output, offset: args.offset, limit: args.limit });
}

export async function grep(args, api) {
  const selectedBackend = await (grepSelection ??= selectGrepBackend(api));
  const result = await (selectedBackend.backend === "rg"
    ? ripgrep(args, api, selectedBackend.program) : gnuGrep(args, api, selectedBackend.program));
  const output = args.output ?? "lines";
  if (output !== "lines" && output !== "files") throw new Error("grep output must be lines or files");
  return { path: args.path ?? ".", output, matches: result.matches,
    backend: selectedBackend.backend,
    ignoreApplied: selectedBackend.backend === "rg" && args.includeIgnored !== true,
    ...(output === "files" ? { files: result.items } : { lines: result.items }),
    offset: result.offset, total: result.total,
    ...(result.nextOffset !== undefined ? { nextOffset: result.nextOffset } : {}),
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
    argv.push("--oneline", "--color=never", `--skip=${offset}`, `--max-count=${limit + 1}`);
    if (args.rev !== undefined) argv.push("--end-of-options", args.rev);
  } else {
    throw new Error("git_inspect op must be status, diff, log, or show");
  }
  argv.push("--", ...(typeof args.paths === "string" ? [args.paths] : args.paths ?? []));
  return api.gitInspect({ op, args: argv, offset: args.offset, limit: args.limit });
}
