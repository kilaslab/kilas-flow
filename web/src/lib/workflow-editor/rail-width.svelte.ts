/**
 * The right rail's one width (FEAT-npc3ge).
 *
 * The inspector opens at 360 px, resizes between 320 and 560, and remembers
 * the choice; the version panel and the node picker render at the same width,
 * so one setting describes every right-rail surface. The value is module state
 * (`$state` in a .svelte.ts module is shared, not per-component) persisted to
 * localStorage as it changes. SSR initialises the default: every surface that
 * shows a width opens after mount (a selected node, an opened panel), so the
 * first paint never disagrees with hydration.
 */

export const RAIL_WIDTH_MIN = 320;
export const RAIL_WIDTH_MAX = 560;
export const RAIL_WIDTH_DEFAULT = 360;
/** Matches --kf-inspector-w in app.css: the token is the design's say, this is the clamp's. */
const STORAGE_KEY = 'kilasflow.rail.width';

function clamp(px: number): number {
	if (!Number.isFinite(px)) return RAIL_WIDTH_DEFAULT;
	return Math.round(Math.min(RAIL_WIDTH_MAX, Math.max(RAIL_WIDTH_MIN, px)));
}

function stored(): number {
	try {
		return clamp(Number(localStorage.getItem(STORAGE_KEY)));
	} catch {
		// Private mode / blocked storage: the default for this session.
		return RAIL_WIDTH_DEFAULT;
	}
}

export const railWidth = $state({ px: typeof localStorage === 'undefined' ? RAIL_WIDTH_DEFAULT : stored() });

export function setRailWidth(px: number): void {
	railWidth.px = clamp(px);
	try {
		localStorage.setItem(STORAGE_KEY, String(railWidth.px));
	} catch {
		// The resize still works for the session; only the memory is lost.
	}
}

/** Arrow-key resize for the separator grip: one step per press. */
export function nudgeRailWidth(delta: number): void {
	setRailWidth(railWidth.px + delta);
}

/**
 * Pointer resize, started from the grip. The rail is docked right, so moving
 * the pointer left widens it. Listeners live on the window for the gesture's
 * span only, and the body keeps its col-resize cursor and no text selection
 * until the pointer comes up.
 */
export function startRailResize(start: PointerEvent): void {
	start.preventDefault();
	const startX = start.clientX;
	const startWidth = railWidth.px;
	document.body.classList.add('kf-rail-resizing');
	const move = (event: PointerEvent) => setRailWidth(startWidth + (startX - event.clientX));
	const up = () => {
		window.removeEventListener('pointermove', move);
		window.removeEventListener('pointerup', up);
		window.removeEventListener('pointercancel', up);
		document.body.classList.remove('kf-rail-resizing');
	};
	window.addEventListener('pointermove', move);
	window.addEventListener('pointerup', up);
	window.addEventListener('pointercancel', up);
}
