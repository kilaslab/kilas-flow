import * as m from '$lib/paraglide/messages.js';

/**
 * The canvas context menu's actions and items (FEAT-ez6xtm).
 *
 * The menu reuses the editor's existing verbs rather than owning a second
 * implementation of any of them: every entry maps onto the same function the
 * keyboard shortcut or the toolbar calls. This table is what both menus are
 * built from, so an action cannot exist in the menu and be missing from the
 * node it was opened on.
 */

/** What a right-click on a node offers. */
export type NodeMenuAction = 'open' | 'rename' | 'duplicate' | 'toggle-disabled' | 'toggle-pinned' | 'copy' | 'delete';

/** What a right-click on empty canvas offers. */
export type PaneMenuAction = 'add-step' | 'add-sticky' | 'paste' | 'select-all';

export type MenuItem = {
	action: NodeMenuAction | PaneMenuAction;
	label: string;
	/** The one destructive entry, drawn in the danger hue and separated. */
	danger?: boolean;
};

/**
 * A node's menu, n8n's minus what this editor puts elsewhere (execute step
 * lives on the toolbar; tidy up on the pane).
 *
 * An annotation (a sticky note) takes no part in a run and has no inspector,
 * so it is offered the document verbs only — rename, duplicate, copy, delete.
 */
export function nodeMenuItems(node: { disabled?: boolean; annotation: boolean; pinned: boolean }): MenuItem[] {
	if (node.annotation) {
		return [
			{ action: 'rename', label: m.canvas_menu_rename() },
			{ action: 'duplicate', label: m.canvas_menu_duplicate() },
			{ action: 'copy', label: m.canvas_menu_copy() },
			{ action: 'delete', label: m.canvas_menu_delete(), danger: true }
		];
	}
	return [
		{ action: 'open', label: m.canvas_menu_open() },
		{ action: 'rename', label: m.canvas_menu_rename() },
		{ action: 'duplicate', label: m.canvas_menu_duplicate() },
		{ action: 'toggle-disabled', label: node.disabled ? m.canvas_menu_enable() : m.canvas_menu_disable() },
		{ action: 'toggle-pinned', label: node.pinned ? m.canvas_menu_unpin() : m.canvas_menu_pin() },
		{ action: 'copy', label: m.canvas_menu_copy() },
		{ action: 'delete', label: m.canvas_menu_delete(), danger: true }
	];
}

/**
 * The pane's menu: what a right-click on empty canvas offers. Everything the
 * keyboard already does with no selection, plus paste — which reports an empty
 * clipboard in the status line rather than hiding the entry, because whether
 * the clipboard holds workflow JSON is not knowable before the click.
 */
export function paneMenuItems(): MenuItem[] {
	return [
		{ action: 'add-step', label: m.canvas_menu_add_step() },
		{ action: 'add-sticky', label: m.canvas_menu_add_sticky() },
		{ action: 'paste', label: m.canvas_menu_paste() },
		{ action: 'select-all', label: m.canvas_menu_select_all() }
	];
}
