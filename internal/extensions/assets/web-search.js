const isRecord = (value) =>
  typeof value === "object" && value !== null && !Array.isArray(value);

const nonBlank = (value) =>
  typeof value === "string" && value.trim().length > 0;

export async function webSearch(args, api) {
  if (!isRecord(args) || !nonBlank(args.query)) {
    throw new Error("expected object with a non-empty query string");
  }

  const response = await api.requestProvider({
    authProfile: "exa-api-key",
    endpoint: "https://api.exa.ai/search",
    method: "POST",
    headers: {
      "content-type": "application/json",
    },
    body: {
      type: "auto",
      numResults: 10,
      ...args,
      contents: { text: false, highlights: { maxCharacters: 1000 } },
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
      ? [parsed.error, parsed.tag, parsed.requestId].filter(nonBlank)
      : [];
    const suffix = details.length ? `: ${details.join(" · ")}` : "";
    throw new Error(`web search provider request failed (${response.status})${suffix}`);
  }
  if (!isRecord(parsed) || !Array.isArray(parsed.results)) {
    throw new Error("web search provider response had no results array");
  }
  return parsed;
}
