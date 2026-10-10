export { catalog } from './modules/catalog.js';

export async function http(args, api) {
  return api.http(args);
}

export async function readFile(args, api) {
  return api.readFile(args);
}

export async function writeFile(args, api) {
  return api.writeFile(args);
}

export async function workflow(args, api) {
  const report = await api.tools.catalog(args);
  const saved = await api.tools.writeFile({ path: args.path, text: JSON.stringify(report, null, 2) });
  const readback = await api.tools.readFile({ path: args.path });
  return { report: JSON.parse(readback.text), savedBytes: saved.bytes };
}
