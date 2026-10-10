import * as definitions from %s;
%s
const execution = %s;

function apiFor(parent) {
  const hostCall = async (op, data) => {
    // RegisterFunc maps a Go (Value, error) return to [value, error].
    const [promise, error] = __host(op, parent, JSON.stringify(data));
    if (error !== null) throw new Error(String(error));
    try { return JSON.parse(await promise); }
    catch (e) { throw new Error(String(e)); }
  };
  return {
    http: data => hostCall('http', data),
    command: data => hostCall('command', data),
    readFile: data => hostCall('readFile', data),
    writeFile: data => hostCall('writeFile', data),
    tools: toolsFor(parent)
  };
}

async function invokeHook(parent, name, data) {
  const id = __start(parent, 'hook', name, JSON.stringify(data));
  try {
    const value = await hooks[name](data, apiFor(id));
    __end(id, 'completed', JSON.stringify(value ?? null), '');
    return value;
  } catch (e) {
    __end(id, 'failed', 'null', String(e));
    throw e;
  }
}

function toolsFor(parent) {
  return Object.fromEntries(Object.keys(definitions).map(name => [name, async (args = {}) => {
    const id = __start(parent, 'tool', name, JSON.stringify(args));
    try {
      if (hooks.beforeTool) args = await invokeHook(id, 'beforeTool', { name, args });
      let value = await definitions[name](args, apiFor(id));
      if (hooks.afterTool) value = await invokeHook(id, 'afterTool', { name, args, result: value });
      __end(id, 'completed', JSON.stringify(value ?? null), '');
      return value;
    } catch (e) {
      __end(id, 'failed', 'null', String(e));
      throw e;
    }
  }]));
}
globalThis.__tools = toolsFor(execution);
