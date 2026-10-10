import { parse } from './vendor/yaml/index.js';

export async function beforeTool({ name, args }, api) {
  if (name !== 'catalog') return args;
  const file = await api.readFile({ path: args.hookConfigPath });
  const config = parse(file.text);
  return { ...args, note: config.note + ':hook-one' };
}

export async function afterTool({ name, result }) {
  return name === 'catalog' ? { ...result, hookRevision: 'hook-one' } : result;
}
