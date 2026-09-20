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

/** What the help overlay lists, in the order it is read. */
export const SHORTCUT_REFERENCE: { keys: string; action: CanvasShortcut; label: string }[] = [
	{ keys: '⌘Z', action: 'undo', label: 'Undo' },
	{ keys: '⌘⇧Z', action: 'redo', label: 'Redo' },
	{ keys: '⌘C', action: 'copy', label: 'Copy selection' },
	{ keys: '⌘V', action: 'paste', label: 'Paste' },
	{ keys: '⌘D', action: 'duplicate', label: 'Duplicate selection' },
	{ keys: 'F2', action: 'rename', label: 'Rename selected node' },
	{ keys: 'Enter', action: 'open-selection', label: 'Open selected node' },
	{ keys: '⌘A', action: 'select-all', label: 'Select all nodes' },
	{ keys: '⌘S', action: 'save', label: 'Save workflow' },
	{ keys: 'Tab / N', action: 'add-step', label: 'Add a step' },
	{ keys: '⌘⇧T', action: 'tidy', label: 'Tidy up' },
	{ keys: '1', action: 'fit-view', label: 'Zoom to fit' },
	{ keys: '0', action: 'reset-zoom', label: 'Reset zoom' },
	{ keys: '+ / -', action: 'zoom-in', label: 'Zoom in / out' },
	{ keys: '?', action: 'help', label: 'Keyboard shortcuts' }
];

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
