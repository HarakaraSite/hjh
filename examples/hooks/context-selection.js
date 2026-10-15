// Select a request with a JS-owned budget; no summarization is performed here.
export async function projectRequest(_, api) {
  const chosen = await api.context.select({
    budget: { inputTokens: 12000, historyTokens: 6000 },
  });
  await api.context.replace(chosen);
}
