import { QueryClient } from '@tanstack/svelte-query';
import { render } from 'svelte/server';
import { describe, expect, it } from 'vitest';

import type { Definition, Node } from '$lib/api/generated/models';

import Harness from './panel-query-harness.svelte';

/**
 * The node state controls in the Settings tab: Disabled, Notes and "Display
 * note in flow". SSR is enough, the way properties-panel.test.ts does it: what
 * is pinned is which controls exist and what they show for a given node, not
 * the event wiring, which is two one-line callbacks into node-state.ts (whose
 * own tests cover what they write).
 */

// No parameters: the panel opens on its Settings tab, where the state lives.
const SET: Definition = {
	category: 'data',
	displayName: 'Set',
	group: ['transform'],
	inputs: [],
	outputs: [],
	parameters: [],
	sharedSettings: [],
	source: 'builtin',
	type: 'kilasflow.set',
	version: 1
};

const STICKY: Definition = { ...SET, displayName: 'Sticky Note', group: ['organization'], type: 'kilasflow.stickyNote' };

function node(extra: Partial<Node> = {}): Node {
	return { id: 'node-1', name: 'Set', type: 'kilasflow.set', typeVersion: 1, position: { x: 0, y: 0 }, ...extra };
}

function html(options: { node?: Node; definition?: Definition; readOnly?: boolean; handlers?: boolean }): string {
	const handlers = options.handlers ?? true;
	const rendered = render(Harness, {
		props: {
			client: new QueryClient({ defaultOptions: { queries: { retry: false } } }),
			panel: {
				node: options.node ?? node(),
				definition: options.definition ?? SET,
				readOnly: options.readOnly ?? false,
				onChange() {},
				...(handlers ? { onDisabledChange() {}, onNoteChange() {} } : {})
			}
		}
	});
	return rendered.body.replace(/<!--.*?-->/g, '');
}

describe('the node state section of the Settings tab', () => {
	it('offers Disabled, Notes and Display note in flow', () => {
		const markup = html({});

		expect(markup).toContain('data-node-state');
		expect(markup).toContain('Node state');
		expect(markup).toContain('Disabled');
		expect(markup).toContain('Notes');
		expect(markup).toContain('Display note in flow');
		expect(markup).toContain('<textarea');
	});

	it('shows an enabled node unchecked and a disabled one checked', () => {
		const checked = (markup: string) => /<input[^>]*data-node-disabled[^>]*>/.exec(markup)?.[0].includes('checked') ?? false;

		expect(checked(html({}))).toBe(false);
		expect(checked(html({ node: node({ disabled: true }) }))).toBe(true);
	});

	it('shows the note and the flag the document holds, where an import put them', () => {
		const markup = html({ node: node({ settings: { notes: 'Only fires out of hours', notesInFlow: true } }) });

		expect(markup).toContain('Only fires out of hours');
		expect(/<input[^>]*data-node-notes-in-flow[^>]*>/.exec(markup)?.[0]).toContain('checked');
	});

	it('shows the flag off when the document does not set it', () => {
		const markup = html({ node: node({ settings: { notes: 'a note' } }) });

		expect(/<input[^>]*data-node-notes-in-flow[^>]*>/.exec(markup)?.[0]).not.toContain('checked');
	});

	it('is not rendered for a sticky note, which takes no part in a run', () => {
		const markup = html({ definition: STICKY, node: node({ type: 'kilasflow.stickyNote' }) });

		expect(markup).not.toContain('data-node-state');
	});

	it('is not rendered when the host gives the panel no way to change either', () => {
		expect(html({ handlers: false })).not.toContain('data-node-state');
	});

	it('sits inside the inert region on a read-only canvas, so it cannot be changed there', () => {
		const markup = html({ readOnly: true });

		expect(markup.indexOf('inert')).toBeGreaterThan(-1);
		expect(markup.indexOf('inert')).toBeLessThan(markup.indexOf('data-node-state'));
	});

	it('replaces the empty-settings message, since the tab now has something to show', () => {
		expect(html({})).not.toContain('No settings');
	});
});
