const isRecord = (value) =>
  typeof value === "object" && value !== null && !Array.isArray(value);

const nonBlank = (value) =>
  typeof value === "string" && value.trim().length > 0;

export async function webSearch(args, api) {
  if (!isRecord(args) || !nonBlank(args.query)) {
    throw new Error("expected object with a non-empty query string");
  }

  const highlights = isRecord(args.contents) ? args.contents.highlights : undefined;
  const dynamicHighlights = isRecord(highlights) &&
    (highlights.dynamic !== undefined || highlights.verbosity !== undefined);
  const response = await api.requestProvider({
    authProfile: "exa-api-key",
    endpoint: "https://api.exa.ai/search",
    method: "POST",
    headers: {
      "content-type": "application/json",
      ...(dynamicHighlights ? { "Exa-Beta": "dynamic-highlights-2026-08-28" } : {}),
    },
    body: {
      type: "auto",
      contents: { highlights: true },
      ...args,
      stream: false,
    },
  });

  const failed = response.status < 200 || response.status >= 300;
  let parsed;
  try {
    parsed = JSON.parse(response.body);
  } catch {
    if (failed) {
      throw new Error(`web search provider request failed (${response.status})`);
    }
    throw new Error("web search provider response was not valid JSON");
  }
  if (failed) {
    const details = isRecord(parsed)
      ? [parsed.error, parsed.tag, parsed.requestId].filter(nonBlank).join(" · ")
      : "";
    throw new Error(`web search provider request failed (${response.status})${details ? `: ${details}` : ""}`);
  }
  if (!isRecord(parsed) || !Array.isArray(parsed.results)) {
    throw new Error("web search provider response had no results array");
  }

  return parsed;
}
