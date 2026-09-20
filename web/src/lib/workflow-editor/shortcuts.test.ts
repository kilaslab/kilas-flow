import { describe, expect, it } from 'vitest';

import { SHORTCUT_REFERENCE, canvasShortcut, controlOwnsKey, isTypingTarget } from './shortcuts';

const press = (key: string, modifiers: Partial<{ metaKey: boolean; ctrlKey: boolean; shiftKey: boolean; altKey: boolean }> = {}) => ({
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
		expect(canvasShortcut(press('t', { metaKey: true, shiftKey: true }))).toBe('tidy');
		expect(canvasShortcut(press('F2'))).toBe('rename');
		expect(canvasShortcut(press('Enter'))).toBe('open-selection');
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
		expect(canvasShortcut(press('x'))).toBeNull();
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
