# web_fetch

Read a known http/https page or API response using {url}. HTML is converted to
readable text. Normal reads retain at most 64 KiB of response bytes before text
conversion and report `truncated: true` when cut. The underlying stream can read
ahead beyond that retained prefix; this is not a network-transfer byte quota. The result is a string with URL,
Status, Content-Type, and truncated header lines followed by the readable body (not
a JSON metadata object). Non-text responses return metadata without a body. Retain
the source URL when presenting evidence.

Omit save_to for reading in model context; do not send an unused empty path.
{url,save_to} instead saves original response bytes to a concrete workspace-relative
or absolute file path without returning the body. For a long page, save it then
read useful file ranges (saved HTML remains HTML, not the converted text). The
parent directory must already exist. A 30-second timeout covers request and body;
interruption leaves any partial file in place. A started filesystem operation can
finish after VM closure; inspect the saved file before retrying.

This run_js example needs web_fetch selected and network access to both known URLs.
Use the task's real established sources; do not add web search just to re-fetch known pages.

```js
// Fetch independent known pages and keep their source URLs.
const urls = ["https://example.com","https://www.iana.org/domains/reserved"];
return await Promise.all(urls.map(async url => ({url,response:await tools.web_fetch({url})})));
```

For precise current facts, fetch the direct canonical source and disclose retrieval
time or conflicts with discovery excerpts. Sources that do not answer the question
are not evidence for a guessed answer.
