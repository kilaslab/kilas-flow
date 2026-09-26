import { QueryClient } from '@tanstack/svelte-query';
import { render } from 'svelte/server';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { Definition, Node, WebhookRouteResource } from '$lib/api/generated/models';
import { getListWorkflowWebhooksQueryKey } from '$lib/api/generated/workflows/workflows';

import Harness from './panel-query-harness.svelte';

/**
 * The panel's webhook address, rendered the way the editor mounts it.
 *
 * The address used to be `/webhook/<path>`, built from what the user typed,
 * which the server answers 404 because the live route is minted. These pin
 * what the panel shows instead. SSR is enough: the lookup's answer is seeded
 * into the query cache the way a completed fetch leaves it, and nothing here
 * depends on an effect running.
 */

const WORKFLOW = 'wf-1';
const ORIGIN = 'https://flow.example.com';
const MINTED_ROUTE = '3225f5b5373e2bc0d5ba95b12512469f';

const WEBHOOK_DEFINITION: Definition = {
	category: 'trigger',
	displayName: 'Webhook',
	group: ['trigger'],
	inputs: [],
	outputs: [],
	// A node with no parameters opens on its Settings tab, where the address is
	// not shown, so the fixture carries the one parameter a webhook always has.
	parameters: [{ key: 'path', label: 'Path', kind: 'string', required: true }],
	sharedSettings: [],
	source: 'builtin',
	type: 'kilasflow.webhook',
	version: 1,
	webhook: { name: 'default', pathParameter: 'path' }
};

function webhookNode(path: string): Node {
	return {
		id: 'node-1',
		name: 'Webhook',
		type: 'kilasflow.webhook',
		typeVersion: 1,
		position: { x: 0, y: 0 },
		parameters: { path }
	};
}

function seededClient(bindings?: WebhookRouteResource[]): QueryClient {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	if (bindings) {
		// Keyed by node as well as workflow, as the panel keys it.
		client.setQueryData([...getListWorkflowWebhooksQueryKey(WORKFLOW), 'node-1'], {
			status: 200,
			data: bindings,
			headers: new Headers()
		});
	}
	return client;
}

const BOUND: WebhookRouteResource[] = [
	{ nodeId: 'node-1', method: 'POST', path: 'orders', url: `/webhook/${MINTED_ROUTE}` }
];

function html(options: {
	client: QueryClient;
	node?: Node;
	savedNode?: Node | null;
	active?: boolean;
	readOnly?: boolean;
}): string {
	const node = options.node ?? webhookNode('orders');
	const rendered = render(Harness, {
		props: {
			client: options.client,
			panel: {
				node,
				definition: WEBHOOK_DEFINITION,
				workflowID: WORKFLOW,
				savedNode: options.savedNode === undefined ? node : options.savedNode,
				active: options.active ?? false,
				readOnly: options.readOnly ?? false,
				onChange() {}
			}
		}
	});
	// SSR leaves hydration markers between elements; they are not markup.
	return rendered.body.replace(/<!--.*?-->/g, '');
}

afterEach(() => vi.unstubAllGlobals());

describe('a webhook node the server has bound', () => {
	it('shows the minted URL with its host, and a copy button', () => {
		vi.stubGlobal('location', { origin: ORIGIN });

		const markup = html({ client: seededClient(BOUND), active: true });

		expect(markup).toContain(`${ORIGIN}/webhook/${MINTED_ROUTE}`);
		expect(markup).toContain('Copy URL');
		expect(markup).toContain('Live');
	});

	it('never shows the path the user typed as if it were the address', () => {
		vi.stubGlobal('location', { origin: ORIGIN });

		const markup = html({ client: seededClient(BOUND), active: true });

		expect(markup).not.toContain('/webhook/orders');
	});

	it('shows the same URL before activation, marked as not live yet', () => {
		vi.stubGlobal('location', { origin: ORIGIN });

		const markup = html({ client: seededClient(BOUND), active: false });

		expect(markup).toContain(`${ORIGIN}/webhook/${MINTED_ROUTE}`);
		expect(markup).toContain('Not live yet');
	});

	it('keeps the address readable and copyable when the editor is read-only', () => {
		vi.stubGlobal('location', { origin: ORIGIN });

		const markup = html({ client: seededClient(BOUND), active: true, readOnly: true });

		// Everything after `inert` is out of reach of a keyboard and a
		// selection, so the address has to come before it.
		expect(markup).toContain('inert');
		expect(markup.indexOf(MINTED_ROUTE)).toBeGreaterThan(-1);
		expect(markup.indexOf(MINTED_ROUTE)).toBeLessThan(markup.indexOf('inert'));
		expect(markup.indexOf('Copy URL')).toBeLessThan(markup.indexOf('inert'));
	});

	it('leaves the editable fields alone when the editor is not read-only', () => {
		vi.stubGlobal('location', { origin: ORIGIN });

		expect(html({ client: seededClient(BOUND) })).not.toContain('inert');
	});
});

describe('a webhook node with no address yet', () => {
	it('says to save the workflow, and shows no URL, when the node was never saved', () => {
		const markup = html({ client: seededClient(), savedNode: null });

		expect(markup).toContain('Save the workflow to get this node');
		expect(markup).not.toContain('/webhook/');
		expect(markup).not.toContain('Copy URL');
	});

	it('says to save the workflow when the path was typed after the last save', () => {
		const markup = html({ client: seededClient(), savedNode: webhookNode('') });

		expect(markup).toContain('Save the workflow to get this node');
		expect(markup).not.toContain('/webhook/');
	});

	it('asks for a path before anything else when the canvas node has none', () => {
		const empty = webhookNode('');
		const markup = html({ client: seededClient(), node: empty, savedNode: empty });

		expect(markup).toContain('Set the path below');
		expect(markup).not.toContain('/webhook/');
	});

	it('says it is looking while the lookup is in flight', () => {
		const markup = html({ client: seededClient() });

		expect(markup).toContain('Looking up the public URL');
		expect(markup).not.toContain('/webhook/');
	});
});
