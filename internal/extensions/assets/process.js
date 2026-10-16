import { execFile } from 'node:child_process';
import { promises as fs } from 'node:fs';
import path from 'node:path';
import process from 'node:process';

// Keep output in the requested encoding, including Uint8Array for binary parsers.
export async function runProcess(program, args = [], options = {}) {
  try {
    const { stdout, stderr } = await execFile(program, args, options);
    return { exitCode: 0, stdout, stderr };
  } catch (error) {
    // v0.25.2 reports launch failures with code 0, not a process exit.
    if (typeof error.code !== 'number' || error.code === 0) throw error;
    return { exitCode: error.code, stdout: error.stdout, stderr: error.stderr };
  }
}

export async function resolveExecutable(program) {
  const candidates = program.includes('/')
    ? [path.resolve(process.cwd(), program)]
    : (process.env.PATH === undefined ? [] : process.env.PATH.split(path.delimiter)
      .map(directory => path.resolve(process.cwd(), directory, program)));
  for (const candidate of candidates) {
    try {
      const stat = await fs.stat(candidate);
      if (stat.isFile() && (stat.mode & 0o111) !== 0) return candidate;
    } catch {
      // PATH search skips unavailable entries and continues in configured order.
    }
  }
  return null;
}
