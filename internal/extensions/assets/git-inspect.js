import { runProcess } from './process.js';

export async function gitInspect(args) {
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
  const offset = args.offset ?? 0;
  const limit = args.limit ?? (op === 'log' ? 20 : 100);
  if (offset < 0 || limit < 1) throw new Error('offset must be nonnegative and limit must be positive');
  const native = await runProcess('git', argv);
  if (native.exitCode !== 0) throw new Error(`git_inspect ${op} failed (${native.exitCode}): ${native.stderr.trim()}`);
  const lines = native.stdout === '' ? [] : native.stdout.replace(/\n$/, '').split('\n');
  const result = { op, text: '', offset };
  if (op === 'log') {
    // Git already applied --skip; only remove the lookahead commit here.
    result.text = lines.slice(0, limit).join('\n');
    if (lines.length > limit) result.nextOffset = offset + limit;
  } else {
    result.total = lines.length;
    const start = Math.min(offset, lines.length);
    const end = Math.min(start + limit, lines.length);
    result.text = lines.slice(start, end).join('\n');
    if (end < lines.length) result.nextOffset = end;
  }
  return result;
}
