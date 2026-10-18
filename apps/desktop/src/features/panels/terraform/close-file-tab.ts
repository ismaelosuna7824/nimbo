/**
 * ⌘W closes a file tab while focus is inside the file viewer, and the workflow otherwise. The top bar owns
 * ⌘W (and on macOS the native menu emits it), so it asks the viewer first through this registry.
 */
let handler: (() => boolean) | null = null

/** Registers the viewer's handler (returns true when it closed a tab); returns the unregister function. */
export function registerCloseFileTab(fn: () => boolean): () => void {
  handler = fn
  return () => {
    if (handler === fn) handler = null
  }
}

/** Closes the active file tab when focus is in the file viewer; false when it did nothing. */
export function closeFocusedFileTab(): boolean {
  return handler?.() ?? false
}
