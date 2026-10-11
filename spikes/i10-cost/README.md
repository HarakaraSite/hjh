# I-10 recall lifetime cost

`go run ./spikes/i10-cost` creates private workspaces and new databases under
`dist/i10-live/cost/`. An injected local provider uses the production root/VM
path to read a 128 KiB file, starts an assistant response, and is cancelled.
Then 1 or 16 normal Core runs prepare and consume that source, actually reach
the provider with one reference, and are cancelled after the request is saved.
No paid provider, credentials, user workspace, or existing DB is touched.

The Manager stays alive at GC checkpoints. The summary separates prepared and
request/settled heap, operation/mirror metadata, relation count, unique saved
reference/wire bytes and bytes referenced across targets. It also measures
allocation from admission through request (including VM, storage and encoding),
and allocation for 64 metadata reads. These are not isolated projection-only
allocations, whole-process RSS or growing canonical-conversation measurements.
Separate live-provider/TUI and cold-read checks establish human usability.
