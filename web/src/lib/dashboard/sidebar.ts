/**
 * Whether the dashboard sidebar is a rail of icons or the full navigation.
 *
 * On the workflow editor the full 13rem sidebar plus the 20rem inspector left
 * about 500 px of canvas at 1024 px, and the selected node disappeared behind
 * the inspector (BUG-bcahaj). Below {@link EDITOR_SIDEBAR_BREAKPOINT} the editor
 * therefore starts with the rail, as n8n's does. The stored preference still
 * governs every other page, and a user who opens the sidebar on the editor
 * keeps it open for the session without that choice overwriting the stored one.
 */

/** Viewport width in px below which the editor collapses the sidebar. */
export const EDITOR_SIDEBAR_BREAKPOINT = 1400;

export function isEditorRoute(pathname: string): boolean {
	return /^\/app\/workflows\/[^/]+$/.test(pathname);
}

export function sidebarCollapsed({
	stored,
	editorRoute,
	compact,
	editorChoice
}: {
	/** The preference stored by the toggle, used away from a narrow editor. */
	stored: boolean;
	editorRoute: boolean;
	/** The viewport is narrower than {@link EDITOR_SIDEBAR_BREAKPOINT}. */
	compact: boolean;
	/** What the user toggled to on a narrow editor this session, or null. */
	editorChoice: boolean | null;
}): boolean {
	if (editorRoute && compact) return editorChoice ?? true;
	return stored;
}
