/**
 * What the editor says about saving, loading and previewing (BUG-hmp85t).
 *
 * The messages had started to expose internals: a conflict printed a second
 * banner quoting both revision ids, and a missing workflow offered "Try again"
 * for something no retry would bring back.
 */

/**
 * The save failure to print, if any.
 *
 * A 409 already has its own banner with the two ways out, so the server's
 * detail — which names the expected and latest revision ids — is not repeated
 * under it.
 */
export function shownSaveError(saveError: string | null, conflict: boolean): string | null {
	return conflict ? null : saveError;
}

/** Whether `error` is an HTTP failure with this status, as the API client raises it. */
export function hasStatus(error: unknown, status: number): boolean {
	return typeof error === 'object' && error !== null && 'status' in error && (error as { status?: unknown }).status === status;
}

const FIT_MARGIN = 24;
/** The least width a fit is allowed to squeeze the revision into. */
const MIN_FIT_WIDTH = 120;

/**
 * Padding for `fitView` that keeps the fitted nodes out from under a panel
 * docked over the canvas's right edge, such as version history.
 *
 * `canvas` is the canvas's horizontal extent on screen and `panelLeft` the
 * panel's left edge, or null when no panel is open.
 */
export function fitPaddingBeside(canvas: { left: number; right: number }, panelLeft: number | null, margin = FIT_MARGIN): { top: string; right: string; bottom: string; left: string } {
	const covered = panelLeft === null ? 0 : Math.max(0, canvas.right - panelLeft);
	const room = canvas.right - canvas.left - margin;
	const right = Math.min(covered + margin, Math.max(margin, room - MIN_FIT_WIDTH));
	return { top: `${margin}px`, right: `${right}px`, bottom: `${margin}px`, left: `${margin}px` };
}
