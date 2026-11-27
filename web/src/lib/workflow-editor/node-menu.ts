/**
 * The node context menu: which actions it offers, for whom, and where it sits.
 *
 * Pure on purpose. The menu component is markup and focus handling; every
 * decision that could be wrong without anyone noticing — a delete offered on a
 * read-only canvas, "Disable" shown for nodes that are all disabled already, a
 * menu opened off the edge of the canvas — is here, where a test can read it.
 *
 * The menu adds no behaviour of its own. Each action names one the editor
 * already has behind a shortcut or a toolbar button, so a node cannot do
 * something from its menu that it cannot do from the keyboard.
 */

import * as m from '$lib/paraglide/messages.js';

export type NodeMenuAction = 'open' | 'rename' | 'duplicate' | 'toggle-disabled' | 'copy' | 'delete';

/** What the menu needs to know about each node it acts on. */
export type NodeMenuTarget = {
	id: string;
	/** A sticky note: it takes no part in a run, so it is never disabled. */
	annotation: boolean;
	disabled: boolean;
};

export type NodeMenuItem = {
	action: NodeMenuAction;
	label: string;
	/** The shortcut that does the same, in the neutral spelling `formatKeys` prints per platform. */
	keys?: string;
	/** Drawn apart from the rest, as the destructive action. */
	danger?: boolean;
	/** A rule above this item, separating the group it starts. */
	separatorBefore?: boolean;
};

/**
 * Whether a key press asks for the context menu of the focused element: the
 * dedicated key, or Shift+F10, which every desktop keyboard has and which
 * Windows and Linux file managers use for the same thing. Another modifier
 * makes it a different chord (Ctrl+F10 is a browser's), so it is not claimed.
 */
export function isContextMenuKey(event: { key: string; shiftKey?: boolean; ctrlKey?: boolean; metaKey?: boolean; altKey?: boolean }): boolean {
	if (event.key === 'ContextMenu') return true;
	return event.key === 'F10' && Boolean(event.shiftKey) && !event.ctrlKey && !event.metaKey && !event.altKey;
}

/**
 * The nodes a context menu acts on.
 *
 * Right-clicking a node inside the current selection acts on the whole
 * selection, which is what a user who selected several and then reached for
 * the menu means. Right-clicking one outside it acts on that node alone, and
 * the editor selects it so the menu and the selection never disagree.
 */
export function menuTargetIDs(selected: readonly string[], clicked: string): string[] {
	return selected.includes(clicked) ? [...selected] : [clicked];
}

/**
 * The items for a menu over `targets`, in the order they are read.
 *
 * An action that does not apply is left out rather than greyed: a disabled
 * "Delete" on a read-only canvas is a control that can only disappoint, and the
 * menu is short enough that a missing line costs nothing. Open and Rename name
 * one node, so they need exactly one; Copy still works on a read-only canvas,
 * because it changes nothing.
 */
export function nodeMenuItems(targets: readonly NodeMenuTarget[], locked: boolean): NodeMenuItem[] {
	if (targets.length === 0) return [];
	const items: NodeMenuItem[] = [];
	const single = targets.length === 1;
	const steps = targets.filter((target) => !target.annotation);

	if (single) items.push({ action: 'open', label: m.canvas_menu_open(), keys: 'Enter' });
	if (single && !locked) items.push({ action: 'rename', label: m.canvas_menu_rename(), keys: 'F2' });
	if (!locked) items.push({ action: 'duplicate', label: m.canvas_menu_duplicate(), keys: 'Mod+D' });
	if (!locked && steps.length > 0) {
		// The same rule as the D key: enable only when every node is already off.
		const enabling = steps.every((target) => target.disabled);
		items.push({
			action: 'toggle-disabled',
			label: enabling ? m.canvas_menu_enable() : m.canvas_menu_disable(),
			keys: 'D'
		});
	}
	items.push({ action: 'copy', label: m.canvas_menu_copy(), keys: 'Mod+C' });
	if (!locked) items.push({ action: 'delete', label: m.canvas_menu_delete(), keys: 'Del', danger: true, separatorBefore: true });
	return items;
}

/**
 * The accessible name of the menu: the node it is for, or how many it acts on.
 */
export function nodeMenuLabel(targets: readonly { id: string }[], names: ReadonlyMap<string, string>): string {
	if (targets.length === 1) return m.canvas_menu_aria_node({ name: names.get(targets[0].id) ?? '' });
	return m.canvas_menu_aria_selection({ count: targets.length });
}

/**
 * The item to focus after `key` in a menu of `count` items, or `current` for a
 * key the menu does not handle. Arrows wrap, as the WAI-ARIA menu pattern asks,
 * and Home/End jump to the ends. `current` is -1 when nothing has focus yet,
 * which makes ArrowDown land on the first item and ArrowUp on the last.
 */
export function nextMenuIndex(count: number, current: number, key: string): number {
	if (count <= 0) return -1;
	if (key === 'Home') return 0;
	if (key === 'End') return count - 1;
	if (key === 'ArrowDown') return current < 0 ? 0 : (current + 1) % count;
	if (key === 'ArrowUp') return current < 0 ? count - 1 : (current - 1 + count) % count;
	return current;
}

export type Point = { x: number; y: number };
export type Size = { width: number; height: number };

/**
 * Where to place a menu opened at `at` so it stays inside `area`.
 *
 * Both are in the same coordinates (the canvas region's own). A menu that would
 * run past the right or bottom edge is pulled back to end `margin` short of it,
 * which is what a native menu does; one larger than the area pins to the
 * margin rather than going negative, so its first item stays reachable.
 */
export function clampMenuPosition(at: Point, menu: Size, area: Size, margin = 8): Point {
	const fit = (position: number, extent: number, limit: number) =>
		Math.max(margin, Math.min(position, limit - extent - margin));
	return { x: fit(at.x, menu.width, area.width), y: fit(at.y, menu.height, area.height) };
}
