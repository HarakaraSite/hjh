import { cp, mkdir, readFile, writeFile, readdir } from 'node:fs/promises';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';
import { build } from 'esbuild';

const here = dirname(fileURLToPath(import.meta.url));
const tools = join(here, '../s03-tools');
const vendor = join(tools, 'vendor');
await mkdir(vendor, { recursive: true });
// Preserve the published ESM's relative import tree, rather than bundling editable tools.
await cp(join(here, 'node_modules/yaml/browser/dist'), join(vendor, 'yaml'), { recursive: true });
await cp(join(here, 'node_modules/yaml/LICENSE'), join(vendor, 'yaml/LICENSE'));
await mkdir(join(vendor, 'semver'), { recursive: true });
await cp(join(here, 'node_modules/semver/LICENSE'), join(vendor, 'semver/LICENSE'));
await cp(join(here, 'node_modules/semver/index.js'), join(vendor, 'semver/raw-index.cjs'));
const bundle = await build({
  absWorkingDir: here,
  stdin: { contents: 'import semver from "semver"; export default semver;', resolveDir: here, sourcefile: 'semver-entry.js' },
  bundle: true, platform: 'neutral', format: 'esm', target: 'es2020',
  outfile: join(vendor, 'semver/index.mjs'), metafile: true,
  legalComments: 'inline'
});
await writeFile(join(vendor, 'semver/build-meta.json'), JSON.stringify(bundle.metafile, null, 2) + '\n');
const lock = JSON.parse(await readFile(join(here, 'package-lock.json'), 'utf8'));
const dependencies = Object.fromEntries(['yaml', 'semver', 'esbuild'].map(name => {
  const pkg = lock.packages[`node_modules/${name}`];
  return [name, { version: pkg.version, resolved: pkg.resolved, integrity: pkg.integrity }];
}));
async function hashes(dir) {
  const files = {};
  for (const entry of (await readdir(dir, { withFileTypes: true })).sort((a,b) => a.name.localeCompare(b.name))) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) Object.assign(files, await hashes(path));
    else if (entry.name !== 'manifest.json') {
      const data = await readFile(path);
      files[relative(vendor, path).split('\\').join('/')] = { bytes: data.length, sha256: createHash('sha256').update(data).digest('hex') };
    }
  }
  return files;
}
const manifest = { dependencies, preparation: 'yaml browser ESM tree; semver CommonJS bundled as neutral ESM', files: await hashes(vendor) };
await writeFile(join(vendor, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
console.log(`Prepared ${Object.keys(manifest.files).length} files at ${vendor}`);
