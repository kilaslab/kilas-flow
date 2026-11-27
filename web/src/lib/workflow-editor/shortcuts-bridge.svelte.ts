// The keyboard shortcuts overlay belongs to the workflow editor, but the Help
// menu that offers it lives in the dashboard sidebar, outside the editor's
// component tree. This is the one shared value between them: the editor marks
// itself available while it is mounted, and the menu asks for the overlay by
// bumping a counter the editor watches. The menu shows the item only while an
// editor is there to answer it (FEAT-p01rcw).
export const shortcutsBridge = $state({ available: false, requests: 0 });

/** Asks the mounted editor to open its keyboard shortcuts overlay. */
export function requestShortcuts(): void {
	shortcutsBridge.requests += 1;
}
