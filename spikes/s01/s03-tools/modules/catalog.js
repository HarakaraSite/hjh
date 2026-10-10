import { parse } from '../vendor/yaml/index.js';
import semver from '../vendor/semver/index.mjs';

export async function catalog(args, api) {
  const response = await api.tools.http({ url: args.url });
  const manifest = parse(response.body);
  const compatible = manifest.dependencies
    .filter(dep => semver.satisfies(dep.candidate, dep.range))
    .map(dep => ({ name: dep.name, version: semver.valid(dep.candidate) }));
  return { project: manifest.project, compatible, note: args.note, toolRevision: 'tool-one' };
}
