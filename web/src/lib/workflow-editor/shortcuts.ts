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
	| 'cut'
	| 'paste'
	| 'duplicate'
	| 'execute'
	| 'toggle-disabled'
	| 'toggle-pinned'
	| 'add-sticky'
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
	/** The physical key, for the combinations a layout rewrites (Option+T is `†` on a Mac). */
	code?: string;
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
 * keys are written once in a neutral spelling (`Mod` is ⌘ on a Mac and Ctrl
 * elsewhere) and printed per platform by formatKeys.
 */
export const SHORTCUT_REFERENCE: { keys: string; action: CanvasShortcut; label: () => string }[] = [
	{ keys: 'Mod+Z', action: 'undo', label: m.editor_shortcut_undo },
	{ keys: 'Mod+Shift+Z', action: 'redo', label: m.editor_shortcut_redo },
	{ keys: 'Mod+C', action: 'copy', label: m.editor_shortcut_copy },
	{ keys: 'Mod+X', action: 'cut', label: m.editor_shortcut_cut },
	{ keys: 'Mod+V', action: 'paste', label: m.editor_shortcut_paste },
	{ keys: 'Mod+D', action: 'duplicate', label: m.editor_shortcut_duplicate },
	{ keys: 'D', action: 'toggle-disabled', label: m.editor_shortcut_toggle_disabled },
	{ keys: 'P', action: 'toggle-pinned', label: m.editor_shortcut_toggle_pinned },
	{ keys: 'F2', action: 'rename', label: m.editor_shortcut_rename },
	{ keys: 'Enter', action: 'open-selection', label: m.editor_shortcut_open },
	{ keys: 'Mod+A', action: 'select-all', label: m.editor_shortcut_select_all },
	{ keys: 'Mod+S', action: 'save', label: m.editor_shortcut_save },
	{ keys: 'Mod+Enter', action: 'execute', label: m.editor_shortcut_execute },
	{ keys: 'Tab / N', action: 'add-step', label: m.editor_shortcut_add_step },
	{ keys: 'Shift+S', action: 'add-sticky', label: m.editor_shortcut_add_sticky },
	{ keys: 'Shift+Alt+T', action: 'tidy', label: m.editor_shortcut_tidy },
	{ keys: '1', action: 'fit-view', label: m.editor_shortcut_fit_view },
	{ keys: '0', action: 'reset-zoom', label: m.editor_shortcut_reset_zoom },
	{ keys: '+ / -', action: 'zoom-in', label: m.editor_shortcut_zoom },
	{ keys: '?', action: 'help', label: m.editor_shortcut_help }
];

const MAC_GLYPHS: Record<string, string> = { Mod: '⌘', Shift: '⇧', Alt: '⌥', Enter: '↵' };
const OTHER_NAMES: Record<string, string> = { Mod: 'Ctrl' };

/**
 * A reference entry's keys as this platform prints them: `⌘⇧Z` on a Mac,
 * `Ctrl+Shift+Z` everywhere else. Alternatives (`Tab / N`) and the bare `+`
 * key pass through untouched.
 */
export function formatKeys(keys: string, mac: boolean): string {
	return keys
		.split(' / ')
		.map((combo) => {
			const parts = combo === '+' ? ['+'] : combo.split('+');
			// A lone Enter reads better spelled out; the glyphs are for chords.
			if (parts.length === 1) return parts[0];
			return mac ? parts.map((part) => MAC_GLYPHS[part] ?? part).join('') : parts.map((part) => OTHER_NAMES[part] ?? part).join('+');
		})
		.join(' / ');
}

/** Whether the browser runs on an Apple platform, where Mod is ⌘. */
export function isMacPlatform(nav: { platform?: string; userAgent?: string } | undefined): boolean {
	if (!nav) return false;
	return /Mac|iPhone|iPad|iPod/.test(nav.platform || '') || /Macintosh|Mac OS X/.test(nav.userAgent || '');
}

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
		if (key === 'Enter') return 'execute';
		if (lower === 'z') return event.shiftKey ? 'redo' : 'undo';
		if (lower === 'y') return 'redo';
		if (lower === 's') return 'save';
		if (lower === 'a') return 'select-all';
		if (lower === 'c') return 'copy';
		if (lower === 'x') return 'cut';
		if (lower === 'v') return 'paste';
		if (lower === 'd') return 'duplicate';
		// Not ⌘⇧T for tidy: Chrome keeps that for "reopen closed tab" and
		// never delivers it to the page.
		return null;
	}

	// Option rewrites the letter on a Mac (Shift+Option+T is `ˇ`), so this
	// one reads the physical key. Any other Alt chord is a typed character.
	if (event.altKey) return event.shiftKey && (event.code === 'KeyT' || lower === 't') ? 'tidy' : null;
	if (event.shiftKey && lower === 's') return 'add-sticky';

	if (key === 'F2') return 'rename';
	if (key === 'Enter') return 'open-selection';
	if (key === 'Tab') return 'add-step';
	if (key === '1') return 'fit-view';
	if (key === '0') return 'reset-zoom';
	if (key === '+' || key === '=') return 'zoom-in';
	if (key === '-' || key === '_') return 'zoom-out';
	if (key === '?') return 'help';
	if (lower === 'n') return 'add-step';
	if (lower === 'd') return 'toggle-disabled';
	if (lower === 'p') return 'toggle-pinned';

	return null;
}

/**
 * The action a keydown asks the editor for, given where it was pressed.
 *
 * Save is the one global key: n8n saves from inside a parameter, and leaving
 * ⌘S to the browser there opened its "Save page as…" dialog. Every other key
 * belongs to the field or button that owns it (controlOwnsKey).
 */
export function editorShortcut(event: ShortcutEvent, target: EventTarget | null): CanvasShortcut | null {
	const action = canvasShortcut(event);
	if (action === 'save') return action;
	if (controlOwnsKey(target, event.key)) return null;
	return action;
}

/**
 * Whether the event came from a control that owns the keystroke.
 *
 * A canvas shortcut must never fire while the user is typing a parameter —
 * apart from save, which editorShortcut lets through on purpose.
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
	if (key === 'Tab') return isNavigableControl(target);
	if (key !== 'Enter') return false;
	const element = target as { tagName?: unknown; closest?: (selector: string) => unknown } | null;
	if (element === null) return false;
	if (element.tagName === 'BUTTON' || element.tagName === 'A') return true;
	return typeof element.closest === 'function' && element.closest('[role="button"],[role="link"]') !== null;
}

const CONTROL_ROLES = ['button', 'link', 'tab', 'menuitem', 'switch', 'checkbox', 'radio', 'option', 'combobox', 'slider', 'spinbutton', 'treeitem']
	.map((role) => `[role="${role}"]`)
	.join(',');

/**
 * Whether Tab on `target` is keyboard navigation rather than the canvas's
 * add-step key. A focused control (toolbar button, inspector field, tab, menu
 * item) moves focus on Tab; the canvas, a node (role="group") and the page do
 * not, so Tab there still opens the node picker. Without this a keyboard user
 * could not leave the first toolbar button (BUG-ywbvfa).
 *
 * Svelte Flow's multi-select box (.svelte-flow__selection-wrapper) is a
 * role="button" too, so Tab moves focus off a marquee selection rather than
 * opening the picker; shortcuts.test.ts pins that.
 */
function isNavigableControl(target: EventTarget | null): boolean {
	const element = target as { tagName?: unknown; closest?: (selector: string) => unknown } | null;
	if (element === null) return false;
	if (element.tagName === 'BUTTON' || element.tagName === 'A' || element.tagName === 'SUMMARY') return true;
	return typeof element.closest === 'function' && element.closest(CONTROL_ROLES) !== null;
}
