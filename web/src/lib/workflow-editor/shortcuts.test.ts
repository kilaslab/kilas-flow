import { describe, expect, it } from 'vitest';

import { SHORTCUT_REFERENCE, canvasShortcut, controlOwnsKey, editorShortcut, formatKeys, isMacPlatform, isTypingTarget } from './shortcuts';

const press = (key: string, modifiers: Partial<{ metaKey: boolean; ctrlKey: boolean; shiftKey: boolean; altKey: boolean; code: string }> = {}) => ({
	key,
	...modifiers
});

describe('canvas keymap', () => {
	it('maps undo and redo to both spellings n8n accepts', () => {
		expect(canvasShortcut(press('z', { metaKey: true }))).toBe('undo');
		expect(canvasShortcut(press('z', { ctrlKey: true }))).toBe('undo');
		expect(canvasShortcut(press('z', { metaKey: true, shiftKey: true }))).toBe('redo');
		expect(canvasShortcut(press('Z', { metaKey: true, shiftKey: true }))).toBe('redo');
		expect(canvasShortcut(press('y', { ctrlKey: true }))).toBe('redo');
	});

	it('maps the editing keys', () => {
		expect(canvasShortcut(press('c', { metaKey: true }))).toBe('copy');
		expect(canvasShortcut(press('v', { metaKey: true }))).toBe('paste');
		expect(canvasShortcut(press('d', { metaKey: true }))).toBe('duplicate');
		expect(canvasShortcut(press('a', { metaKey: true }))).toBe('select-all');
		expect(canvasShortcut(press('s', { metaKey: true }))).toBe('save');
		expect(canvasShortcut(press('F2'))).toBe('rename');
		expect(canvasShortcut(press('Enter'))).toBe('open-selection');
	});

	// n8n's own map (UXE-10): the keys an n8n user presses without looking.
	it('maps the n8n keys for running, cutting, disabling and annotating', () => {
		expect(canvasShortcut(press('Enter', { metaKey: true }))).toBe('execute');
		expect(canvasShortcut(press('Enter', { ctrlKey: true }))).toBe('execute');
		expect(canvasShortcut(press('x', { metaKey: true }))).toBe('cut');
		expect(canvasShortcut(press('x', { ctrlKey: true }))).toBe('cut');
		expect(canvasShortcut(press('d'))).toBe('toggle-disabled');
		expect(canvasShortcut(press('D'))).toBe('toggle-disabled');
		expect(canvasShortcut(press('S', { shiftKey: true, code: 'KeyS' }))).toBe('add-sticky');
		// A plain S is not a sticky: only the shifted key is.
		expect(canvasShortcut(press('s'))).toBeNull();
	});

	// Cmd+Shift+T is Chrome's "reopen closed tab" and never reaches the page.
	// Option turns T into a dead glyph on a Mac, so the physical key is read.
	it('tidies on Shift+Alt+T, by the physical key, and not on Cmd+Shift+T', () => {
		expect(canvasShortcut(press('ˇ', { shiftKey: true, altKey: true, code: 'KeyT' }))).toBe('tidy');
		expect(canvasShortcut(press('T', { shiftKey: true, altKey: true, code: 'KeyT' }))).toBe('tidy');
		expect(canvasShortcut(press('t', { metaKey: true, shiftKey: true, code: 'KeyT' }))).toBeNull();
		// Alt alone with a letter is a typed character on some layouts, not a shortcut.
		expect(canvasShortcut(press('∂', { altKey: true, code: 'KeyD' }))).toBeNull();
	});

	it('maps the viewport and discovery keys', () => {
		expect(canvasShortcut(press('1'))).toBe('fit-view');
		expect(canvasShortcut(press('0'))).toBe('reset-zoom');
		expect(canvasShortcut(press('+'))).toBe('zoom-in');
		expect(canvasShortcut(press('='))).toBe('zoom-in');
		expect(canvasShortcut(press('-'))).toBe('zoom-out');
		expect(canvasShortcut(press('Tab'))).toBe('add-step');
		expect(canvasShortcut(press('n'))).toBe('add-step');
		expect(canvasShortcut(press('N'))).toBe('add-step');
		expect(canvasShortcut(press('?'))).toBe('help');
	});

	it('claims the keys it lists in the help overlay, and nothing else', () => {
		const claimed: Record<string, true> = {};
		for (const entry of SHORTCUT_REFERENCE) {
			claimed[entry.action] = true;
			expect(entry.keys).not.toBe('');
		}
		expect(claimed.undo).toBe(true);
		expect(claimed.help).toBe(true);

		// A Cmd combination the canvas does not handle must not fall through to
		// the plain-letter meaning: Cmd+N is the browser's new window.
		expect(canvasShortcut(press('n', { metaKey: true }))).toBeNull();
		expect(canvasShortcut(press('k', { metaKey: true }))).toBeNull();
		expect(canvasShortcut(press('Escape'))).toBeNull();
		expect(canvasShortcut(press('q'))).toBeNull();
		expect(canvasShortcut(press('Shift'))).toBeNull();
	});
});

describe('isTypingTarget', () => {
	it('is true for the controls that own the keyboard', () => {
		expect(isTypingTarget({ tagName: 'INPUT' } as unknown as EventTarget)).toBe(true);
		expect(isTypingTarget({ tagName: 'TEXTAREA' } as unknown as EventTarget)).toBe(true);
		expect(isTypingTarget({ tagName: 'SELECT' } as unknown as EventTarget)).toBe(true);
		expect(isTypingTarget({ tagName: 'DIV', isContentEditable: true } as unknown as EventTarget)).toBe(true);
		// A plain element inside a rich-text host inherits the editable flag.
		expect(
			isTypingTarget({ tagName: 'SPAN', closest: (selector: string) => (selector.includes('contenteditable') ? {} : null) } as unknown as EventTarget)
		).toBe(true);
	});

	it('is false for the canvas itself', () => {
		expect(isTypingTarget({ tagName: 'DIV' } as unknown as EventTarget)).toBe(false);
		expect(isTypingTarget(null)).toBe(false);
	});
});

describe('controlOwnsKey', () => {
	it('gives every key to a field the user is typing in', () => {
		expect(controlOwnsKey({ tagName: 'INPUT' } as unknown as EventTarget, 'Enter')).toBe(true);
		expect(controlOwnsKey({ tagName: 'TEXTAREA' } as unknown as EventTarget, 's')).toBe(true);
		expect(controlOwnsKey({ tagName: 'SELECT' } as unknown as EventTarget, 'Enter')).toBe(true);
	});

	// Enter opens the selected node on the canvas and activates a focused
	// button or link. Without this the editor took the press the user aimed at
	// the control.
	it('gives Enter to the button or link that has focus', () => {
		expect(controlOwnsKey({ tagName: 'BUTTON' } as unknown as EventTarget, 'Enter')).toBe(true);
		expect(controlOwnsKey({ tagName: 'A' } as unknown as EventTarget, 'Enter')).toBe(true);
		// A control built from a div is still a button to the keyboard.
		expect(
			controlOwnsKey(
				{ tagName: 'SPAN', closest: (selector: string) => (selector.includes('role="button"') ? {} : null) } as unknown as EventTarget,
				'Enter'
			)
		).toBe(true);
	});

	// Only Enter is contended: a button does not own Cmd+S, and the canvas
	// shortcut for it has to keep working while one has focus.
	it('leaves the canvas keys a focused button does not own', () => {
		expect(controlOwnsKey({ tagName: 'BUTTON' } as unknown as EventTarget, 's')).toBe(false);
		expect(controlOwnsKey({ tagName: 'A' } as unknown as EventTarget, 'z')).toBe(false);
		expect(controlOwnsKey({ tagName: 'DIV' } as unknown as EventTarget, 'Enter')).toBe(false);
		expect(controlOwnsKey(null, 'Enter')).toBe(false);
	});
});

// UXE-11: Cmd+S inside a parameter opened the browser's "Save page as…".
describe('editorShortcut', () => {
	const input = { tagName: 'INPUT' } as unknown as EventTarget;
	const pane = { tagName: 'DIV' } as unknown as EventTarget;

	it('saves from inside a field the user is typing in', () => {
		expect(editorShortcut(press('s', { metaKey: true }), input)).toBe('save');
		expect(editorShortcut(press('s', { ctrlKey: true }), input)).toBe('save');
	});

	it('leaves every other key to the field', () => {
		expect(editorShortcut(press('a', { metaKey: true }), input)).toBeNull();
		expect(editorShortcut(press('z', { metaKey: true }), input)).toBeNull();
		expect(editorShortcut(press('x', { metaKey: true }), input)).toBeNull();
		expect(editorShortcut(press('d'), input)).toBeNull();
		expect(editorShortcut(press('Enter', { metaKey: true }), input)).toBeNull();
	});

	it('is the canvas keymap everywhere else', () => {
		expect(editorShortcut(press('a', { metaKey: true }), pane)).toBe('select-all');
		expect(editorShortcut(press('d'), pane)).toBe('toggle-disabled');
		expect(editorShortcut(press('Enter'), { tagName: 'BUTTON' } as unknown as EventTarget)).toBeNull();
	});
});

// UXE-10: the overlay printed ⌘ on every OS.
describe('formatKeys', () => {
	it('prints Mac glyphs on a Mac', () => {
		expect(formatKeys('Mod+Shift+Z', true)).toBe('⌘⇧Z');
		expect(formatKeys('Shift+Alt+T', true)).toBe('⇧⌥T');
		expect(formatKeys('Mod+Enter', true)).toBe('⌘↵');
	});

	it('prints Ctrl and named modifiers elsewhere', () => {
		expect(formatKeys('Mod+Shift+Z', false)).toBe('Ctrl+Shift+Z');
		expect(formatKeys('Shift+Alt+T', false)).toBe('Shift+Alt+T');
		expect(formatKeys('Mod+Enter', false)).toBe('Ctrl+Enter');
	});

	it('keeps plain keys and alternatives as they are', () => {
		expect(formatKeys('+ / -', false)).toBe('+ / -');
		expect(formatKeys('Tab / N', true)).toBe('Tab / N');
		expect(formatKeys('F2', false)).toBe('F2');
	});

	it('lists every reference entry in both spellings', () => {
		for (const entry of SHORTCUT_REFERENCE) {
			expect(formatKeys(entry.keys, true)).not.toContain('Mod');
			expect(formatKeys(entry.keys, false)).not.toContain('⌘');
		}
	});
});

describe('isMacPlatform', () => {
	it('reads the platform the browser reports', () => {
		expect(isMacPlatform({ platform: 'MacIntel', userAgent: '' })).toBe(true);
		expect(isMacPlatform({ platform: 'iPad', userAgent: '' })).toBe(true);
		expect(isMacPlatform({ platform: 'Win32', userAgent: 'Mozilla/5.0 (Windows NT 10.0)' })).toBe(false);
		expect(isMacPlatform({ platform: 'Linux x86_64', userAgent: 'Mozilla/5.0 (X11; Linux x86_64)' })).toBe(false);
		expect(isMacPlatform({ platform: '', userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)' })).toBe(true);
		expect(isMacPlatform(undefined)).toBe(false);
	});
});
