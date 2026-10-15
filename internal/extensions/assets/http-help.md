# http

Use {url} for a raw HTTP GET response with status and body. Use web_fetch when
you want readable page text or HTML conversion; use web_search for discovery when
the source is not yet known. A returned HTTP status should be inspected alongside
the body rather than treating a successful tool invocation as a successful page.

This run_js body needs http selected and network access to the example endpoint.
Replace it with the task's already established canonical URL when appropriate.

```js
// Read a known endpoint as a raw response.
return await tools.http({url:"https://example.com"});
```
