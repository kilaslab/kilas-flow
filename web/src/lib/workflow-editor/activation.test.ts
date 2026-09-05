import { describe, expect, it } from 'vitest';

import type { ActivationNotice, Node } from '$lib/api/generated/models';
import { ApiError } from '$lib/api/http';
import {
	activationFailure,
	activationNotices,
	copyableURL,
	dismissNotice
} from './activation';

// The exact sentence packs/waha/manifest-202502.json declares, with the route
// substituted in the way GatedLifecycle.ActivationNotice substitutes it.
const wahaNotice: ActivationNotice = {
	nodeId: 'trigger',
	nodeType: 'kilasflow.waha.trigger',
	message:
		'WAHA is not configured to deliver here yet. Add https://flows.example.test/webhook/abc123 to this session\'s webhook configuration, or turn on "Register this URL with WAHA" and activate again.'
};

const nodes: Node[] = [
	{
		id: 'trigger',
		name: 'WhatsApp message received',
		type: 'kilasflow.waha.trigger',
		typeVersion: 202502,
		position: { x: 0, y: 0 }
	}
];

describe('activationNotices', () => {
	it('names the notice after the node the user named on the canvas', () => {
		const [notice] = activationNotices([wahaNotice], nodes);
		expect(notice.nodeName).toBe('WhatsApp message received');
	});

	it('falls back to the node ID when the notice is about a node the document does not hold', () => {
		const [notice] = activationNotices([{ ...wahaNotice, nodeId: 'trigger_2' }], nodes);
		expect(notice.nodeName).toBe('trigger_2');
	});

	it('falls back to the node type when the server named no node at all', () => {
		const [notice] = activationNotices([{ ...wahaNotice, nodeId: '' }], nodes);
		expect(notice.nodeName).toBe('kilasflow.waha.trigger');
	});

	it('offers the URL a WAHA notice tells the user to paste', () => {
		const [notice] = activationNotices([wahaNotice], nodes);
		expect(notice.url).toBe('https://flows.example.test/webhook/abc123');
	});

	it('shows nothing for a workflow whose triggers needed nothing', () => {
		expect(activationNotices([], nodes)).toEqual([]);
	});

	it('shows nothing when the response omitted the notices array', () => {
		expect(activationNotices(null, nodes)).toEqual([]);
		expect(activationNotices(undefined, nodes)).toEqual([]);
	});

	it('reads a document that carries no nodes without failing', () => {
		const [notice] = activationNotices([wahaNotice], null);
		expect(notice.nodeName).toBe('trigger');
	});

	it('gives two notices from the same node keys that can be dismissed apart', () => {
		const both = activationNotices([wahaNotice, { ...wahaNotice, message: 'Something else.' }], nodes);
		expect(new Set(both.map((notice) => notice.key)).size).toBe(2);
	});
});

describe('dismissNotice', () => {
	it('leaves the other notices from the same activation standing', () => {
		const both = activationNotices([wahaNotice, { ...wahaNotice, message: 'Something else.' }], nodes);
		const remaining = dismissNotice(both, both[0].key);
		expect(remaining).toHaveLength(1);
		expect(remaining[0].message).toBe('Something else.');
	});
});

describe('copyableURL', () => {
	it('pulls the URL out of the sentence the notice states it in', () => {
		expect(copyableURL('Add https://flows.example.test/webhook/abc123 to the session.')).toBe(
			'https://flows.example.test/webhook/abc123'
		);
	});

	it('drops the full stop from a URL that ends the sentence', () => {
		expect(copyableURL('Paste https://flows.example.test/webhook/abc123.')).toBe(
			'https://flows.example.test/webhook/abc123'
		);
	});

	it('drops the closing bracket from a URL stated inside one', () => {
		expect(copyableURL('The route (https://flows.example.test/webhook/abc123) is not registered.')).toBe(
			'https://flows.example.test/webhook/abc123'
		);
	});

	it('keeps a query string intact', () => {
		expect(copyableURL('Use https://flows.example.test/webhook/abc?token=xyz&mode=push now.')).toBe(
			'https://flows.example.test/webhook/abc?token=xyz&mode=push'
		);
	});

	it('offers nothing to copy when the notice names no URL', () => {
		expect(copyableURL('Connect a credential to this trigger before activating it.')).toBeNull();
	});

	it('offers nothing to copy for a bare scheme nobody could paste anywhere', () => {
		expect(copyableURL('Configure https:// somewhere else.')).toBeNull();
	});
});

describe('activationFailure', () => {
	it('says the workflow was left inactive when a trigger could not register', () => {
		const failure = activationFailure(new ApiError(502, 'waha: setWebhook returned 401'));
		expect(failure).toContain('left inactive');
		expect(failure).toContain('waha: setWebhook returned 401');
		expect(failure).toContain('502');
	});

	it('reports any other activation failure with the status a bug report can quote', () => {
		expect(activationFailure(new ApiError(404, 'Workflow not found'))).toBe('404 — Workflow not found');
	});

	it('reports a failure that never reached the server', () => {
		expect(activationFailure(new TypeError('Failed to fetch'))).toBe('Failed to fetch');
	});
});
