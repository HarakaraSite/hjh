export { listDirectory, findFiles, countFiles } from './inspection.js';
export { gitInspect } from './git-inspect.js';
import { grepPage } from './grep-parser.js';
import { resolveExecutable, runProcess } from './process.js';

let grepSelection;
async function selectGrepBackend() {
  const rg = await resolveExecutable("rg");
  if (rg) return { backend: "rg", program: rg };
  const grep = await resolveExecutable("grep");
  if (grep) return { backend: "grep", program: grep };
  throw new Error("grep requires rg or GNU grep on PATH");
}

// --json retains names containing newlines and records matching/context lines.
async function ripgrep(args, program) {
  const argv = ["--json", "--hidden", "--color", "never"];
  if (args.patternKind === "literal") argv.push("--fixed-strings");
  if (args.caseSensitive === false) argv.push("--ignore-case");
  if (args.includeIgnored === true) argv.push("--no-ignore");
  if (args.context !== undefined) argv.push("--context", String(args.context));
  for (const glob of typeof args.glob === "string" ? [args.glob] : args.glob ?? []) argv.push("--glob", glob);
  argv.push("--regexp", args.pattern, "--", args.path ?? ".");
  return runProcess(program, argv, { encoding: 'buffer' });
}

async function gnuGrep(args, program) {
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
  return runProcess(program, argv, { encoding: 'buffer' });
}

export async function grep(args) {
  const output = args.output ?? "lines";
  if (output !== "lines" && output !== "files") throw new Error("grep output must be lines or files");
  const offset = args.offset ?? 0;
  const limit = args.limit ?? 100;
  if (offset < 0 || limit < 1) throw new Error('offset must be nonnegative and limit must be positive');
  const selectedBackend = await (grepSelection ??= selectGrepBackend());
  const native = await (selectedBackend.backend === "rg"
    ? ripgrep(args, selectedBackend.program) : gnuGrep(args, selectedBackend.program));
  const warnings = new TextDecoder().decode(native.stderr);
  if (native.exitCode > 1) throw new Error(`grep failed (${native.exitCode}): ${warnings.trim()}`);
  const result = grepPage(native.stdout, selectedBackend.backend, output, offset, limit);
  return { path: args.path ?? ".", output, matches: result.matches,
    backend: selectedBackend.backend,
    ignoreApplied: selectedBackend.backend === "rg" && args.includeIgnored !== true,
    ...(output === "files" ? { files: result.items } : { lines: result.items }),
    offset: result.offset, total: result.total,
    ...(result.nextOffset !== undefined ? { nextOffset: result.nextOffset } : {}),
    ...(warnings ? { warnings } : {}) };
}
