# web_search

Discover sources with Exa using a specific query. A query alone defaults to auto,
up to 10 results, and highlights of at most 1000 characters per result. Page text is
disabled; use web_fetch to read selected page bodies. Do not send contents options.
Results are complete JSON (no transport streaming), with ordered URLs, titles,
short highlights, metadata, and optional synthesized output.

## Source-first workflow

1. If the user names the repository, local file, canonical URL, or API, use it first.
   Search only when it leaves an external/current question unresolved.
2. For external/current information whose target identity or canonical source is
   unknown, start with web_search. Do not inspect workspace/sibling repositories,
   handoff files, or guessed bash/curl endpoints just because a workspace exists.
   When external sources suffice, stay on that route.
3. Give a specific query. Send only options needed for that search; omit unused
   values rather than empty strings, null, [], or guessed filters. Request fewer
   results if that is enough. On failure, use the provider's actual error to choose
   the next action rather than changing unrelated options.
4. Read selected URLs with web_fetch. Independent known-page reads can use Promise.all;
   discovery-dependent retrieval follows discovery. For long pages, save original
   bytes with save_to, then read_file ranges. Get fast-changing lists and precise
   current values from the direct canonical source, disclosing retrieval time and
   conflicts with search excerpts.
5. Cite direct source URLs near supported claims, not provider-local citation
   numbers. results[].highlights are source excerpts; output is synthesis, not a
   substitute for source evidence. Preserve source identifiers when aggregating.
   Label inference and say when sources do not answer; do not fill gaps with facts
   unsupported by returned material.

## Options beyond the default query

Choose type for search depth/speed (instant, fast, auto, deep-lite, deep,
deep-reasoning); additionalQueries belongs to deep search modes. includeDomains and
excludeDomains constrain sources. startPublishedDate/endPublishedDate use ISO 8601.
category accepts standard or custom hints; company and people do not support
published-date filters or excludeDomains. userLocation accepts a two-letter country
code or an object with country, latitude, longitude. numResults availability varies
by mode. Consult the current schema for the full option list.

Use outputSchema only when synthesized structured output is needed: root type text
for text, or an object with properties (up to 10 properties and 2 nesting levels).
objective gives broader factual needs and systemPrompt adds synthesis instructions.
These are synthesis controls, not page-body retrieval options.

## Discovery with retained evidence

This async run_js body requires web_search selected, configured Exa credentials,
and network access. It returns the original search result rather than assuming all
response variants contain a particular collection. Follow the discovered source
URLs with web_fetch only after inspecting the evidence.

```js
// Discover official documentation sources for a specific external question.
return await tools.web_search({query:"site:go.dev/doc Go modules reference replace directive",numResults:3});
```
