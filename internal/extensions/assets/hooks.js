// Export lifecycle hooks as needed. Returning { context: [...] } from runtimeStart or
// beforeTurn adds those strings to this Worker's or turn's model instruction.
// Export beforeTool and/or afterTool to change tool arguments or results.

// Export projectRequest({ runtime, modelStep }, api) to inspect or replace each
// model request through api.context.get/history/measure/select/replace. Changes affect
// that request only; its return value is ignored. See docs/design/request-context.md.
