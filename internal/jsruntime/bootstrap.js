function implementationApiFor(parent) {
  const hostCall = async (operation, input) => {
    try {
      const encoded = await __host(operation, parent, JSON.stringify(input ?? {}));
      return JSON.parse(encoded);
    } catch (error) {
      throw error instanceof Error ? error : new Error(String(error));
    }
  };
  const agentCall = async input => {
    try {
      const encoded = await __agent(parent, JSON.stringify(input ?? {}));
      return JSON.parse(encoded);
    } catch (error) {
      throw error instanceof Error ? error : new Error(String(error));
    }
  };
  const agentControlCall = async (operation, input) => {
    try {
      const encoded = await __agentControl(operation, parent, JSON.stringify(input ?? {}));
      return JSON.parse(encoded);
    } catch (error) {
      throw error instanceof Error ? error : new Error(String(error));
    }
  };
  const contextCall = async (operation, input) => {
    const encoded = await __context(operation, parent, JSON.stringify(input ?? {}));
    return JSON.parse(encoded);
  };
  return {
    model: { generate: input => contextCall('model.generate', input) },
    context: {
      get: () => contextCall('get'),
      history: input => contextCall('history', input),
      measure: input => contextCall('measure', input),
      select: input => contextCall('select', input),
      replace: input => contextCall('replace', input),
      memory: {
        get: input => contextCall('memory.get', input),
        set: input => contextCall('memory.set', input),
      },
    },
    requestProvider: input => hostCall('requestProvider', input),
    agents: {
      run: agentCall,
      spawn: input => agentControlCall('spawn', input),
      status: input => agentControlCall('status', input),
      steer: input => agentControlCall('steer', input),
      collect: input => agentControlCall('collect', input),
      cancel: input => agentControlCall('cancel', input),
    },
    tools: toolsFor(parent),
  };
}

function generatedApiFor(parent) {
  const tools = toolsFor(parent);
  const api = { tools };
  const agents = {};
  if (globalThis.__selectedToolNames.includes('run_agent')) agents.run = tools.run_agent;
  if (globalThis.__selectedToolNames.includes('spawn_agent')) agents.spawn = tools.spawn_agent;
  if (globalThis.__selectedToolNames.includes('agent_status')) agents.status = tools.agent_status;
  if (globalThis.__selectedToolNames.includes('steer_agent')) agents.steer = tools.steer_agent;
  if (globalThis.__selectedToolNames.includes('collect_agent')) agents.collect = tools.collect_agent;
  if (globalThis.__selectedToolNames.includes('cancel_agent')) agents.cancel = tools.cancel_agent;
  if (Object.keys(agents).length) api.agents = agents;
  return api;
}

function encode(value) {
  return JSON.stringify(value === undefined ? null : value);
}

async function invokeHook(parent, name, input) {
  const hook = globalThis.__extensionHooks?.[name];
  if (typeof hook !== 'function') return input;
  const id = __start(parent, 'hook', name, encode(input));
  try {
    const value = await hook(input, implementationApiFor(id));
    __end(id, 'completed', encode(value), '');
    return value;
  } catch (error) {
    __end(id, 'failed', 'null', String(error));
    throw error;
  }
}

async function invokeLifecycleHook(name, input, id) {
  const hook = globalThis.__extensionHooks?.[name];
  if (typeof hook !== 'function') return 'null';
  const value = await hook(JSON.parse(input), implementationApiFor(id));
  // Go owns the lifecycle root outcome after draining all started work.
  // A request hook changes its candidate through api.context. Its return
  // value may contain a transcript; do not duplicate that in hook history.
  return name === 'projectRequest' ? 'null' : encode(value);
}

async function invokeTool(name, originalArgs, parent, record) {
  let args = originalArgs ?? {};
  const id = record ? __start(parent, 'tool', name, encode(args)) : parent;
  let ended = false;
  try {
    if (!globalThis.__selectedToolNames.includes(name)) throw new Error(`unknown extension tool: ${name}`);
    if (args.help === true) {
      const help = globalThis.__extensionHelp[name];
      if (help.error) throw new Error(help.error);
      const result = { ...help };
      if (record) {
        __end(id, 'completed', encode(result), '');
        ended = true;
      }
      return result;
    }
    if (globalThis.__extensionHooks?.beforeTool) {
      args = await invokeHook(id, 'beforeTool', { name, args });
    }
    let result;
    if (name === 'run_js') {
      if (typeof args.code !== 'string') throw new TypeError('run_js requires a string code field');
      const invoke = new Function('tools', 'api', `return (async function () { ${args.code}\n }).call(undefined);`);
      const api = generatedApiFor(id);
      result = await invoke(api.tools, api);
    } else {
      const implementation = globalThis.__extensionTools[name];
      if (typeof implementation !== 'function') throw new Error(`unknown extension tool: ${name}`);
      result = await implementation(args, implementationApiFor(id));
    }
    if (globalThis.__extensionHooks?.afterTool) {
      result = await invokeHook(id, 'afterTool', { name, args, result });
    }
    if (record) {
      __end(id, 'completed', encode(result), '');
      ended = true;
    }
    return result;
  } catch (error) {
    if (record && !ended) {
      try { __end(id, 'failed', 'null', String(error)); } catch (_) {}
    }
    throw error;
  }
}

function toolsFor(parent) {
  return Object.fromEntries(globalThis.__selectedToolNames.map(name => [name, async (args = {}) =>
    invokeTool(name, args, parent, true)]));
}

globalThis.__invokeDirect = async function (name, args, parent) {
  return encode(await invokeTool(name, JSON.parse(args), parent, false));
};

globalThis.__invokeLifecycleHook = invokeLifecycleHook;

globalThis.__runGenerated = async function (code, parent) {
  return encode(await invokeTool('run_js', { code }, parent, false));
};
