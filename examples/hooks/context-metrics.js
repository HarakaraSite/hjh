// Measure the history-inclusive request without changing its input or budget.
export async function projectRequest({ runtime, modelStep }, api) {
  const view = await api.context.get();
  const size = await api.context.measure();
  const observation = {
    scope: "request-before-and-after-selection",
    sessionId: runtime.sessionId,
    executionId: runtime.executionId,
    turnNumber: runtime.turnNumber,
    modelStep,
    provider: runtime.model.provider,
    modelId: runtime.model.modelId,
    effort: runtime.model.effort,
    currentMessageCount: view.messages.length,
    toolCount: view.tools.length,
    ...size,
  };
  await api.writeFile({
    path: `.hjh/context-metrics/${runtime.executionId}-${modelStep}.json`,
    text: JSON.stringify(observation, null, 2) + "\n",
  });
}
