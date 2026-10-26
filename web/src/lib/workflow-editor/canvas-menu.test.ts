import { describe, expect, it } from 'vitest';

import { nodeMenuItems, paneMenuItems, type MenuItem } from './canvas-menu';

/** The menu entries an item list offers, by action — labels are locale-loaded. */
const actions = (items: MenuItem[]) => items.map((item) => item.action);

describe('nodeMenuItems (FEAT-ez6xtm)', () => {
	// n8n's node menu: open, rename, duplicate, deactivate, pin, copy, delete.
	// Ours reuses the verbs the keyboard and the toolbar already own.
	it('offers the seven verbs on a step, delete last', () => {
		const items = nodeMenuItems({ annotation: false, disabled: false, pinned: false });
		expect(actions(items)).toEqual(['open', 'rename', 'duplicate', 'toggle-disabled', 'toggle-pinned', 'copy', 'delete']);
		expect(items.at(-1)?.danger).toBe(true);
		expect(items.filter((item) => item.danger)).toHaveLength(1);
	});

	it('flips the disable and pin labels with the node\'s state, so the menu always offers the other side', () => {
		const enabled = nodeMenuItems({ annotation: false, disabled: false, pinned: false });
		const disabled = nodeMenuItems({ annotation: false, disabled: true, pinned: true });
		expect(enabled.find((item) => item.action === 'toggle-disabled')?.label).not.toBe(disabled.find((item) => item.action === 'toggle-disabled')?.label);
		expect(enabled.find((item) => item.action === 'toggle-pinned')?.label).not.toBe(disabled.find((item) => item.action === 'toggle-pinned')?.label);
		// The actions themselves are stable: the handler dispatches on what the
		// node is, not on what the label said.
		expect(actions(enabled)).toEqual(actions(disabled));
	});

	it('offers an annotation the document verbs only — nothing to open, disable or pin', () => {
		const items = nodeMenuItems({ annotation: true, disabled: false, pinned: false });
		expect(actions(items)).toEqual(['rename', 'duplicate', 'copy', 'delete']);
	});
});

describe('paneMenuItems', () => {
	it('offers what the keyboard does with no selection, plus paste', () => {
		expect(actions(paneMenuItems())).toEqual(['add-step', 'add-sticky', 'paste', 'select-all']);
	});
});
