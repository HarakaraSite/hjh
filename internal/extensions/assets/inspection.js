import { promises as fs } from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { compileGlob } from './glob.js';

function entryType(stat) {
  if (stat.isSymbolicLink()) return 'symlink';
  if (stat.isDirectory()) return 'directory';
  return stat.isFile() ? 'file' : 'other';
}

function addEntry(result, entry, limit) {
  if (result.total >= result.offset && result.entries.length < limit) result.entries.push(entry);
  result.total++;
}

function finishPage(result) {
  if (result.offset + result.entries.length < result.total) result.nextOffset = result.offset + result.entries.length;
  return result;
}

export async function listDirectory(args) {
  const display = args.path || '.';
  const tree = args.tree ?? false;
  const depth = args.depth ?? 3;
  const offset = args.offset ?? 0;
  const limit = args.limit ?? 100;
  if (offset < 0 || depth < 1 || limit < 1) throw new Error('ls expects nonnegative offset and positive depth and limit');
  const result = { path: display, tree, entries: [], offset, total: 0 };
  async function visit(actual, display, level) {
    // stdlib readdir returns sorted names, including dot entries.
    for (const name of await fs.readdir(actual)) {
      const filename = path.join(actual, name);
      const stat = await fs.lstat(filename);
      const type = entryType(stat);
      const entry = { name, path: path.join(display, name), type, bytes: stat.size, modifiedAt: stat.mtime, depth: level };
      addEntry(result, entry, limit);
      if (tree && type === 'directory' && level < depth) await visit(filename, entry.path, level + 1);
    }
  }
  await visit(path.resolve(process.cwd(), display), display, 1);
  return finishPage(result);
}

export async function findFiles(args) {
  const display = args.path || '.';
  const type = args.type || 'any';
  const offset = args.offset ?? 0;
  const limit = args.limit ?? 100;
  if (offset < 0 || limit < 1) throw new Error('find expects nonnegative offset and positive limit');
  if (!['any', 'file', 'directory'].includes(type)) throw new Error('find type must be any, file, or directory');
  const matches = compileGlob(args.pattern || '*');
  const excludes = (args.exclude ?? []).map(compileGlob);
  const result = { path: display, entries: [], offset, total: 0 };
  const root = path.resolve(process.cwd(), display);
  async function visit(actual, relative, stat) {
    const kind = entryType(stat);
    if (relative !== '' || kind !== 'directory') {
      const name = relative || path.basename(actual);
      if (excludes.some(exclude => exclude(name))) return;
      if (matches(name) && (type === 'any' || type === kind)) addEntry(result, { path: actual, type: kind }, limit);
    }
    if (kind === 'directory') {
      for (const name of await fs.readdir(actual)) {
        const filename = path.join(actual, name);
        await visit(filename, relative ? `${relative}/${name}` : name, await fs.lstat(filename));
      }
    }
  }
  await visit(root, '', await fs.lstat(root));
  return finishPage(result);
}

// unicode.IsSpace (Go): notably includes NEL U+0085, but not BOM U+FEFF.
function isSpace(rune) {
  return (rune >= 0x09 && rune <= 0x0d) || rune === 0x20 || rune === 0x85 || rune === 0xa0
    || rune === 0x1680 || (rune >= 0x2000 && rune <= 0x200a) || rune === 0x2028
    || rune === 0x2029 || rune === 0x202f || rune === 0x205f || rune === 0x3000;
}

export async function countFiles(args) {
  const result = { files: [], total: { lines: 0, words: 0, bytes: 0 } };
  for (const filename of args.paths ?? []) {
    const bytes = await fs.readFile(path.resolve(process.cwd(), filename));
    const counts = { path: filename, lines: 0, words: 0, bytes: bytes.length };
    for (const byte of bytes) if (byte === 0x0a) counts.lines++;
    const text = new TextDecoder('utf-8', { ignoreBOM: true }).decode(bytes);
    let inWord = false;
    for (const char of text) {
      const space = isSpace(char.codePointAt(0));
      if (!space && !inWord) counts.words++;
      inWord = !space;
    }
    result.files.push(counts);
    for (const key of ['lines', 'words', 'bytes']) result.total[key] += counts[key];
  }
  return result;
}
