import { promises as fs } from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { runProcess } from './process.js';

export async function readFile(args) {
  const offset = args.offset ?? 1;
  if (offset < 1 || (args.limit !== undefined && args.limit < 1)) {
    throw new Error("read_file offset and limit must be positive line counts");
  }
  const text = await fs.readFile(path.resolve(process.cwd(), args.path), 'utf8');
  // Keep each LF with its line, including CRLF and an unterminated final line.
  const lines = text.match(/[^\n]*\n|[^\n]+$/g) ?? [];
  const from = Math.min(offset - 1, lines.length);
  const to = args.limit === undefined ? lines.length : from + Math.min(args.limit, lines.length - from);
  const result = { path: args.path, text: lines.slice(from, to).join(''), offset, lines: to - from, totalLines: lines.length };
  if (to < lines.length) result.nextOffset = to + 1;
  return result;
}

export async function writeFile(args) {
  const filename = path.resolve(process.cwd(), args.path);
  const bytes = new TextEncoder().encode(args.text);
  await fs.mkdir(path.dirname(filename), { recursive: true });
  await fs.writeFile(filename, bytes);
  return { path: args.path, bytes: bytes.length };
}

export async function editFile(args) {
  if (!Array.isArray(args.edits)) throw new Error("edit_file requires an edits array of {oldText, newText} replacements");
  const filename = path.resolve(process.cwd(), args.path);
  let text = await fs.readFile(filename, 'utf8');
  const result = { path: args.path, replacements: 0, edits: [] };
  for (const { oldText, newText } of args.edits) {
    let replacements = 0;
    if (oldText !== '') {
      const parts = text.split(oldText);
      replacements = parts.length - 1;
      text = parts.join(newText);
    }
    result.edits.push({ replacements });
    result.replacements += replacements;
  }
  const bytes = new TextEncoder().encode(text);
  await fs.writeFile(filename, bytes);
  result.bytes = bytes.length;
  return result;
}

export async function command(args) {
  return runProcess(args.program, args.args ?? [], { input: args.stdin ?? "" });
}

export { http, webFetch } from './web-fetch.js';
