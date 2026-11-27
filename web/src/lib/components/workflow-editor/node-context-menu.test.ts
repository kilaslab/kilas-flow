import { render } from 'svelte/server';
import { describe, expect, it } from 'vitest';

import { nodeMenuItems } from '$lib/workflow-editor/node-menu';

import NodeContextMenu from './node-context-menu.svelte';

/**
 * The menu's markup contract for assistive technology: a named `menu` whose
 * rows are `menuitem`s, with the destructive one set apart by a separator.
 * SSR can only prove what is rendered. The decisions behind focus and
 * placement (arrow wrapping, clamping, which items exist) are pure and tested
 * in node-menu.test.ts; Escape, outside press and focus return need a browser
 * and were not exercised by a test.
 */

function html(options: { items?: ReturnType<typeof nodeMenuItems>; mac?: boolean } = {}): string {
	const items = options.items ?? nodeMenuItems([{ id: 'a', annotation: false, disabled: false }], false);
	const rendered = render(NodeContextMenu, {
		props: {
			at: { x: 40, y: 60 },
			area: { width: 800, height: 600 },
			items,
			label: 'Actions for Set',
			mac: options.mac ?? false,
			onSelect() {},
			onClose() {}
		}
	});
	return rendered.body.replace(/<!--.*?-->/g, '');
}

describe('the node context menu markup', () => {
	it('is a named menu', () => {
		const markup = html();

		expect(markup).toContain('role="menu"');
		expect(markup).toContain('aria-label="Actions for Set"');
		expect(markup).toContain('aria-orientation="vertical"');
	});

	it('renders one menuitem button per action, in order, none in the tab order', () => {
		const markup = html();
		const actions = [...markup.matchAll(/data-menu-action="([^"]+)"/g)].map((match) => match[1]);

		expect(actions).toEqual(['open', 'rename', 'duplicate', 'toggle-disabled', 'copy', 'delete']);
		expect(markup.match(/role="menuitem"/g)).toHaveLength(6);
		expect(markup.match(/role="menuitem" tabindex="-1"/g)).toHaveLength(6);
		expect(markup).not.toContain('role="menuitem" tabindex="0"');
	});

	it('names the actions and sets the destructive one apart', () => {
		const markup = html();

		for (const label of ['Open', 'Rename', 'Duplicate', 'Disable', 'Copy', 'Delete']) expect(markup).toContain(`>${label}</span>`);
		expect(markup).toContain('role="separator"');
		expect(markup.indexOf('role="separator"')).toBeLessThan(markup.indexOf('data-menu-action="delete"'));
	});

	it('prints each shortcut for the platform', () => {
		expect(html({ mac: false })).toContain('>Ctrl+D</kbd>');
		expect(html({ mac: true })).toContain('>⌘D</kbd>');
	});

	it('is placed where it was asked for', () => {
		expect(html()).toContain('left: 40px; top: 60px');
	});
});
