# I-09 input lifetime cost

`go run ./spikes/i09-cost` uses new private workspaces under `dist/i09-live/cost/`.
It holds a local provider request, admits either 1 or 64 inputs with identical
128 KiB text, then cancels and waits for the input chain to settle. No credential,
external provider, user workspace, or existing database is used or deleted.

`summary.json` records database size, Go heap after GC, shared body copies,
receipt metadata bytes and the small terminal control snapshot. The Manager
remains alive at the heap checkpoints. Each saved input must have a durable
cancel reason and all identical input bodies must reference one contents row.

This measures admission and cancellation costs. It does not measure growing
model context, provider request snapshots, replay materialization, or whole
process RSS. The live-provider and cold-replay checks are recorded separately
in [the I-09 results](../../docs/results/i09-runtime-input.md).
