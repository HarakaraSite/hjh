export async function spawnAgent(args, api) {
  return api.agents.spawn(args);
}

export async function agentStatus(args, api) {
  return api.agents.status(args);
}

export async function steerAgent(args, api) {
  return api.agents.steer(args);
}

export async function collectAgent(args, api) {
  return api.agents.collect(args);
}

export async function cancelAgent(args, api) {
  return api.agents.cancel(args);
}
