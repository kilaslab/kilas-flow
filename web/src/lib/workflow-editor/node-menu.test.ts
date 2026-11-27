import { describe, expect, it } from 'vitest';

import { clampMenuPosition, isContextMenuKey, menuTargetIDs, nextMenuIndex, nodeMenuItems, nodeMenuLabel, type NodeMenuTarget } from './node-menu';

function target(id: string, extra: Partial<NodeMenuTarget> = {}): NodeMenuTarget {
	return { id, annotation: false, disabled: false, ...extra };
}

const actions = (targets: NodeMenuTarget[], locked = false) => nodeMenuItems(targets, locked).map((item) => item.action);

describe('isContextMenuKey', () => {
	it('claims the context-menu key and Shift+F10', () => {
		expect(isContextMenuKey({ key: 'ContextMenu' })).toBe(true);
		expect(isContextMenuKey({ key: 'F10', shiftKey: true })).toBe(true);
	});

	it('leaves bare F10 and other F10 chords to the browser', () => {
		expect(isContextMenuKey({ key: 'F10' })).toBe(false);
		expect(isContextMenuKey({ key: 'F10', shiftKey: true, ctrlKey: true })).toBe(false);
		expect(isContextMenuKey({ key: 'F10', shiftKey: true, metaKey: true })).toBe(false);
		expect(isContextMenuKey({ key: 'F10', shiftKey: true, altKey: true })).toBe(false);
		expect(isContextMenuKey({ key: 'd' })).toBe(false);
	});
});

describe('menuTargetIDs', () => {
	it('acts on the whole selection when the clicked node is in it', () => {
		expect(menuTargetIDs(['a', 'b', 'c'], 'b')).toEqual(['a', 'b', 'c']);
	});

	it('acts on the clicked node alone when it is outside the selection', () => {
		expect(menuTargetIDs(['a', 'b'], 'z')).toEqual(['z']);
		expect(menuTargetIDs([], 'z')).toEqual(['z']);
	});

	it('hands back a copy, so the editor’s selection is never the menu’s to change', () => {
		const selected = ['a', 'b'];
		expect(menuTargetIDs(selected, 'a')).not.toBe(selected);
	});
});

describe('nodeMenuItems', () => {
	it('offers open, rename, duplicate, disable, copy and delete for one node, in that order', () => {
		expect(actions([target('a')])).toEqual(['open', 'rename', 'duplicate', 'toggle-disabled', 'copy', 'delete']);
	});

	it('labels the toggle for what it will do', () => {
		const label = (targets: NodeMenuTarget[]) => nodeMenuItems(targets, false).find((item) => item.action === 'toggle-disabled')?.label;
		expect(label([target('a')])).toBe('Disable');
		expect(label([target('a', { disabled: true })])).toBe('Enable');
		// The D key's rule: enable only when every node is already off.
		expect(label([target('a', { disabled: true }), target('b')])).toBe('Disable');
		expect(label([target('a', { disabled: true }), target('b', { disabled: true })])).toBe('Enable');
	});

	it('leaves out open and rename for several nodes, which name no single one', () => {
		expect(actions([target('a'), target('b')])).toEqual(['duplicate', 'toggle-disabled', 'copy', 'delete']);
	});

	it('leaves out the toggle for a sticky note, which takes no part in a run', () => {
		expect(actions([target('s', { annotation: true })])).toEqual(['open', 'rename', 'duplicate', 'copy', 'delete']);
	});

	it('judges a mixed selection by its steps: stickies neither count nor block the toggle', () => {
		const items = nodeMenuItems([target('s', { annotation: true }), target('a', { disabled: true })], false);
		expect(items.find((item) => item.action === 'toggle-disabled')?.label).toBe('Enable');
	});

	it('offers only what changes nothing on a read-only canvas', () => {
		expect(actions([target('a')], true)).toEqual(['open', 'copy']);
		expect(actions([target('a'), target('b')], true)).toEqual(['copy']);
	});

	it('is empty when there is nothing to act on', () => {
		expect(nodeMenuItems([], false)).toEqual([]);
	});

	it('marks delete as the destructive item and sets it apart', () => {
		const items = nodeMenuItems([target('a')], false);
		const remove = items.find((item) => item.action === 'delete');
		expect(remove?.danger).toBe(true);
		expect(remove?.separatorBefore).toBe(true);
		expect(items.filter((item) => item.danger)).toHaveLength(1);
	});

	it('carries the shortcut that does the same, which the editor already binds', () => {
		const keys = Object.fromEntries(nodeMenuItems([target('a')], false).map((item) => [item.action, item.keys]));
		expect(keys).toEqual({ open: 'Enter', rename: 'F2', duplicate: 'Mod+D', 'toggle-disabled': 'D', copy: 'Mod+C', delete: 'Del' });
	});
});

describe('nodeMenuLabel', () => {
	it('names the node for one target and counts the selection for several', () => {
		const names = new Map([['a', 'HTTP Request']]);
		expect(nodeMenuLabel([{ id: 'a' }], names)).toBe('Actions for HTTP Request');
		expect(nodeMenuLabel([{ id: 'a' }, { id: 'b' }], names)).toBe('Actions for 2 selected nodes');
	});
});

describe('nextMenuIndex', () => {
	it('moves down and up, wrapping at both ends', () => {
		expect(nextMenuIndex(4, 0, 'ArrowDown')).toBe(1);
		expect(nextMenuIndex(4, 3, 'ArrowDown')).toBe(0);
		expect(nextMenuIndex(4, 1, 'ArrowUp')).toBe(0);
		expect(nextMenuIndex(4, 0, 'ArrowUp')).toBe(3);
	});

	it('starts at the first item going down and the last going up when nothing has focus', () => {
		expect(nextMenuIndex(4, -1, 'ArrowDown')).toBe(0);
		expect(nextMenuIndex(4, -1, 'ArrowUp')).toBe(3);
	});

	it('jumps to the ends with Home and End', () => {
		expect(nextMenuIndex(4, 2, 'Home')).toBe(0);
		expect(nextMenuIndex(4, 1, 'End')).toBe(3);
	});

	it('keeps the current item for a key it does not handle, and answers -1 for an empty menu', () => {
		expect(nextMenuIndex(4, 2, 'a')).toBe(2);
		expect(nextMenuIndex(0, -1, 'ArrowDown')).toBe(-1);
	});
});

describe('clampMenuPosition', () => {
	const area = { width: 800, height: 600 };
	const menu = { width: 180, height: 200 };

	it('leaves a menu that fits where it was opened', () => {
		expect(clampMenuPosition({ x: 100, y: 120 }, menu, area)).toEqual({ x: 100, y: 120 });
	});

	it('pulls a menu back from the right and bottom edges', () => {
		expect(clampMenuPosition({ x: 790, y: 590 }, menu, area)).toEqual({ x: 800 - 180 - 8, y: 600 - 200 - 8 });
	});

	it('keeps a menu off the left and top edges', () => {
		expect(clampMenuPosition({ x: -20, y: 2 }, menu, area)).toEqual({ x: 8, y: 8 });
	});

	it('pins a menu larger than the area to the margin so its first item stays reachable', () => {
		expect(clampMenuPosition({ x: 50, y: 50 }, { width: 900, height: 700 }, area)).toEqual({ x: 8, y: 8 });
	});
});
