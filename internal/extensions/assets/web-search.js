const isRecord = (value) =>
  typeof value === "object" && value !== null && !Array.isArray(value);

const nonBlank = (value) =>
  typeof value === "string" && value.trim().length > 0;

export async function webSearch(args, api) {
  if (!isRecord(args) || !nonBlank(args.query)) {
    throw new Error("expected object with a non-empty query string");
  }

  return api.webSearch({
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
}
