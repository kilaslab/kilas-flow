/**
 * The canvas keymap.
 *
 * One table, read from the outside in — the editor never asks "was that
 * Ctrl+Shift+Z?" anywhere else, so a shortcut cannot exist in the keyboard
 * handler and be missing from the help overlay (which renders this list).
 *
 * Keys follow n8n's, because the editor's users arrive with that muscle memory:
 * `?` lists them, and `0`/`1`/`+`/`-` are the viewport keys everyone already
 * knows from a map.
 */

import * as m from '$lib/paraglide/messages.js';

export type CanvasShortcut =
	| 'undo'
	| 'redo'
	| 'save'
	| 'select-all'
	| 'copy'
	| 'paste'
	| 'duplicate'
	| 'rename'
	| 'open-selection'
	| 'fit-view'
	| 'reset-zoom'
	| 'zoom-in'
	| 'zoom-out'
	| 'tidy'
	| 'add-step'
	| 'help';

export type ShortcutEvent = {
	key: string;
	metaKey?: boolean;
	ctrlKey?: boolean;
	shiftKey?: boolean;
	altKey?: boolean;
};

/**
 * What the help overlay lists, in the order it is read.
 *
 * `label` is a call rather than the sentence itself so the overlay follows a
 * locale change at runtime — the table is built once at module load, and text
 * captured at that moment would stay in whatever locale was active then. The
 * keys stay literals: a key label reads the same in every locale.
 */
export const SHORTCUT_REFERENCE: { keys: string; action: CanvasShortcut; label: () => string }[] = [
	{ keys: '⌘Z', action: 'undo', label: m.editor_shortcut_undo },
	{ keys: '⌘⇧Z', action: 'redo', label: m.editor_shortcut_redo },
	{ keys: '⌘C', action: 'copy', label: m.editor_shortcut_copy },
	{ keys: '⌘V', action: 'paste', label: m.editor_shortcut_paste },
	{ keys: '⌘D', action: 'duplicate', label: m.editor_shortcut_duplicate },
	{ keys: 'F2', action: 'rename', label: m.editor_shortcut_rename },
	{ keys: 'Enter', action: 'open-selection', label: m.editor_shortcut_open },
	{ keys: '⌘A', action: 'select-all', label: m.editor_shortcut_select_all },
	{ keys: '⌘S', action: 'save', label: m.editor_shortcut_save },
	{ keys: 'Tab / N', action: 'add-step', label: m.editor_shortcut_add_step },
	{ keys: '⌘⇧T', action: 'tidy', label: m.editor_shortcut_tidy },
	{ keys: '1', action: 'fit-view', label: m.editor_shortcut_fit_view },
	{ keys: '0', action: 'reset-zoom', label: m.editor_shortcut_reset_zoom },
	{ keys: '+ / -', action: 'zoom-in', label: m.editor_shortcut_zoom },
	{ keys: '?', action: 'help', label: m.editor_shortcut_help }
];

/**
 * The keys the canvas deletes a selection with, in the shape Svelte Flow's
 * `deleteKey` prop takes.
 *
 * Deletion itself is Svelte Flow's, but which physical keys mean it on this
 * canvas belongs with the rest of the keymap — which is here, beside the
 * reference the help overlay reads.
 */
export const CANVAS_DELETE_KEYS: string[] = ['Backspace', 'Delete'];

/**
 * The action a key event asks for, or null for a key the canvas ignores.
 *
 * Modifiers are deliberately exact: an unhandled `⌘`-combination returns null
 * rather than falling through to the plain-letter meaning, so `⌘N` cannot open
 * the node picker in a browser where the user meant "new window".
 */
export function canvasShortcut(event: ShortcutEvent): CanvasShortcut | null {
	const mod = Boolean(event.metaKey || event.ctrlKey);
	const key = event.key;
	const lower = key.toLocaleLowerCase();

	if (mod) {
		// Shift is only meaningful for redo; a shifted copy is still a copy.
		if (lower === 'z') return event.shiftKey ? 'redo' : 'undo';
		if (lower === 'y') return 'redo';
		if (lower === 's') return 'save';
		if (lower === 'a') return 'select-all';
		if (lower === 'c') return 'copy';
		if (lower === 'v') return 'paste';
		if (lower === 'd') return 'duplicate';
		if (event.shiftKey && lower === 't') return 'tidy';
		return null;
	}

	if (key === 'F2') return 'rename';
	if (key === 'Enter') return 'open-selection';
	if (key === 'Tab') return 'add-step';
	if (key === '1') return 'fit-view';
	if (key === '0') return 'reset-zoom';
	if (key === '+' || key === '=') return 'zoom-in';
	if (key === '-' || key === '_') return 'zoom-out';
	if (key === '?') return 'help';
	if (lower === 'n' && !event.altKey) return 'add-step';

	return null;
}

/**
 * Whether the event came from a control that owns the keystroke.
 *
 * A canvas shortcut must never fire while the user is typing a parameter, so
 * everything is suppressed — including `⌘S`, which would otherwise save
 * mid-word and look like the editor eating the key.
 *
 * Read through property checks rather than `instanceof HTMLElement`: the field
 * it is testing is the tag name and the editable flag, and this way the rule is
 * one function instead of one function per environment.
 */
export function isTypingTarget(target: EventTarget | null): boolean {
	if (target === null) return false;
	const element = target as { tagName?: unknown; isContentEditable?: unknown; closest?: (selector: string) => unknown };
	if (element.isContentEditable === true) return true;
	const tag = element.tagName;
	if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true;
	// A read-only input still owns the keyboard: arrow keys move the caret.
	return typeof element.closest === 'function' && element.closest('[contenteditable="true"]') !== null;
}

/**
 * Whether a focused control owns this keystroke, so the canvas must leave it
 * alone.
 *
 * Typing controls own everything, as above. A focused button or link owns
 * Enter: it activates on that key, and Enter is also the canvas key that opens
 * the selected node, so without this the editor swallowed the press — the
 * properties panel opened instead of the control the user had tabbed to. Space
 * activates a button too, but the canvas claims no Space, so nothing contends
 * for it.
 *
 * Read through the tag name and roles rather than `instanceof`: same reason as
 * isTypingTarget, and a control built from a div with role="button" is a button
 * to the keyboard however it is spelled in markup.
 */
export function controlOwnsKey(target: EventTarget | null, key: string): boolean {
	if (isTypingTarget(target)) return true;
	if (key !== 'Enter') return false;
	const element = target as { tagName?: unknown; closest?: (selector: string) => unknown } | null;
	if (element === null) return false;
	if (element.tagName === 'BUTTON' || element.tagName === 'A') return true;
	return typeof element.closest === 'function' && element.closest('[role="button"],[role="link"]') !== null;
}
