import { promises as fs } from "node:fs";
import path from "node:path";

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
  const filename = path.resolve(process.cwd(), `.hjh/context-metrics/${runtime.executionId}-${modelStep}.json`);
  await fs.mkdir(path.dirname(filename), { recursive: true });
  await fs.writeFile(filename, JSON.stringify(observation, null, 2) + "\n");
}
