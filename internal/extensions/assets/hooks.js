// Export lifecycle hooks as needed. Returning { context: [...] } from runtimeStart or
// beforeTurn adds those strings to this Worker's or turn's model instruction.
export async function runtimeStart({ runtime }) {
  return { context: [`Agent runtime started at: ${runtime.startedAt} (${runtime.timeZone})`] };
}

// Export beforeTool and/or afterTool to change tool arguments or results.
