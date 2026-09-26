import { describe, expect, it } from 'vitest';

import type { Node, WebhookDeclaration, WebhookRouteResource } from '$lib/api/generated/models';

import { absoluteWebhookURL, savedNodeBinds, webhookAddress, webhookPathOf } from './webhook-address';

const DECLARATION: WebhookDeclaration = { name: 'default', pathParameter: 'path', methodParameter: 'httpMethod' };
const ORIGIN = 'https://flow.example.com';

function webhookNode(path: string | undefined, extra: Partial<Node> = {}): Node {
	return {
		id: 'node-1',
		name: 'Webhook',
		type: 'kilasflow.webhook',
		typeVersion: 1,
		position: { x: 0, y: 0 },
		...(path === undefined ? {} : { parameters: { path } }),
		...extra
	};
}

const MINTED: WebhookRouteResource = {
	nodeId: 'node-1',
	method: 'POST',
	path: 'orders',
	url: '/webhook/3225f5b5373e2bc0d5ba95b12512469f'
};

function address(overrides: Partial<Parameters<typeof webhookAddress>[0]> = {}) {
	return webhookAddress({
		declaration: DECLARATION,
		node: webhookNode('orders'),
		saved: webhookNode('orders'),
		bindings: [MINTED],
		failed: false,
		origin: ORIGIN,
		active: true,
		...overrides
	});
}

describe('a saved webhook node the server has bound', () => {
	it('shows the minted URL under the origin the editor was served from', () => {
		expect(address()).toEqual({
			kind: 'ready',
			url: 'https://flow.example.com/webhook/3225f5b5373e2bc0d5ba95b12512469f',
			live: true
		});
	});

	it('is the minted route and never the path the user typed', () => {
		const shown = address();

		expect(shown.kind).toBe('ready');
		expect(JSON.stringify(shown)).not.toContain('/webhook/orders');
	});

	it('is shown before activation too, marked as not live yet', () => {
		// The route is minted on first read and reused when the workflow is
		// activated, so the URL is real before it answers anything.
		expect(address({ active: false })).toMatchObject({ kind: 'ready', live: false });
	});

	it('finds the node by id when the workflow has several webhooks', () => {
		const other: WebhookRouteResource = { nodeId: 'node-2', method: 'GET', path: 'other', url: '/webhook/ffff' };

		expect(address({ bindings: [other, MINTED] })).toMatchObject({
			url: 'https://flow.example.com/webhook/3225f5b5373e2bc0d5ba95b12512469f'
		});
	});
});

describe('a webhook node with no address to show', () => {
	it('asks for a path when the canvas node has none, however the saved one reads', () => {
		expect(address({ node: webhookNode('') })).toEqual({ kind: 'needs-path' });
		expect(address({ node: webhookNode(undefined) })).toEqual({ kind: 'needs-path' });
		expect(address({ node: webhookNode('   ') })).toEqual({ kind: 'needs-path' });
	});

	it('asks for a save when the saved revision lacks the node, and shows no URL at all', () => {
		const shown = address({ saved: null, bindings: undefined });

		expect(shown).toEqual({ kind: 'needs-save' });
		expect(JSON.stringify(shown)).not.toContain('/webhook/');
	});

	it('asks for a save when the path was typed after the last save', () => {
		expect(address({ saved: webhookNode('') })).toEqual({ kind: 'needs-save' });
	});

	it('asks for a save while the saved node is disabled, because the server does not bind it', () => {
		expect(address({ saved: webhookNode('orders', { disabled: true }) })).toEqual({ kind: 'needs-save' });
	});

	it('is loading until the lookup answers', () => {
		expect(address({ bindings: undefined })).toEqual({ kind: 'loading' });
	});

	it('is unavailable when the lookup failed, and does not fall back to a guess', () => {
		expect(address({ failed: true, bindings: undefined })).toEqual({ kind: 'unavailable' });
	});

	it('is unbound when the server answered without this node', () => {
		expect(address({ bindings: [] })).toEqual({ kind: 'unbound' });
		expect(address({ bindings: [{ ...MINTED, nodeId: 'someone-else' }] })).toEqual({ kind: 'unbound' });
	});
});

describe('webhookPathOf', () => {
	it('reads the declared parameter', () => {
		expect(webhookPathOf(DECLARATION, webhookNode(' orders '))).toBe('orders');
	});

	it('falls back to the fixed path of a declaration with no parameter', () => {
		expect(webhookPathOf({ name: 'bot', staticPath: 'telegram' }, webhookNode(undefined))).toBe('telegram');
	});

	it('is empty for a node the saved revision does not have', () => {
		expect(webhookPathOf(DECLARATION, null)).toBe('');
	});
});

describe('savedNodeBinds', () => {
	it('needs the node saved, enabled and with a path, as the server does', () => {
		expect(savedNodeBinds(DECLARATION, webhookNode('orders'))).toBe(true);
		expect(savedNodeBinds(DECLARATION, null)).toBe(false);
		expect(savedNodeBinds(DECLARATION, webhookNode(''))).toBe(false);
		expect(savedNodeBinds(DECLARATION, webhookNode('orders', { disabled: true }))).toBe(false);
	});

	it('binds a fixed-path trigger from the declaration alone', () => {
		expect(savedNodeBinds({ name: 'bot', staticPath: 'telegram' }, webhookNode(undefined))).toBe(true);
	});
});

describe('absoluteWebhookURL', () => {
	it('adds the origin to the server route', () => {
		expect(absoluteWebhookURL('/webhook/abc', 'https://flow.example.com')).toBe('https://flow.example.com/webhook/abc');
	});

	it('does not double a slash', () => {
		expect(absoluteWebhookURL('/webhook/abc', 'https://flow.example.com/')).toBe('https://flow.example.com/webhook/abc');
		expect(absoluteWebhookURL('webhook/abc', 'https://flow.example.com')).toBe('https://flow.example.com/webhook/abc');
	});

	it('leaves an address that already has a host alone', () => {
		expect(absoluteWebhookURL('https://hooks.example.org/webhook/abc', 'https://flow.example.com')).toBe(
			'https://hooks.example.org/webhook/abc'
		);
	});
});
