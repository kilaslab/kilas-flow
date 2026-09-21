import { render } from 'svelte/server';
import { describe, expect, it } from 'vitest';

import type { PropertyDefinition } from '$lib/api/generated/models';

import PropertyField from './property-field.svelte';

/**
 * The field's server-rendered markup.
 *
 * An `options` property is a control whose whole value set is declared, so it
 * belongs to a select and not to a free-text box: the customer types a value
 * the node will refuse, or picks one the loader would have offered, and finds
 * out only when the workflow runs. These render observations pin the control
 * kind, the label's target and the value the control shows; the browser
 * behaviour (a loader answering, an edit round-tripping) is the e2e suite's.
 *
 * SSR is the right level here because it needs no DOM: `$effect` never runs
 * on the server, so a loader-backed property stays at the value it was given,
 * which is exactly how a saved node renders before its loader answers.
 */
function html(property: PropertyDefinition, value: unknown): string {
	const rendered = render(PropertyField, {
		props: { property, value, onChange() {} }
	});
	// SSR leaves hydration markers between elements; they are not markup.
	return rendered.body.replace(/<!--.*?-->/g, '');
}

const RESOURCE: PropertyDefinition = {
	key: 'resource',
	label: 'Resource',
	kind: 'options',
	required: false,
	options: [
		{ label: 'Message', value: 'message' },
		{ label: 'Mailbox', value: 'mailbox' }
	]
};

describe('an options property', () => {
	it('renders a select carrying every declared option', () => {
		const markup = html(RESOURCE, 'mailbox');

		expect(markup).toContain('<select');
		expect(markup).toContain('<option value="message">Message</option>');
		expect(markup).toContain('<option value="mailbox" selected="">Mailbox</option>');
		expect(markup).not.toMatch(/<input[^>]*type="text"/);
	});

	it('the label of an options property points at the select it renders', () => {
		const markup = html(RESOURCE, 'mailbox');
		const target = markup.match(/for="(property-[^"]+)"/)?.[1];

		expect(target).toBeDefined();
		expect(markup).toContain(`<select id="${target}"`);
	});

	it('a stored value that is not in the list stays visible and selected', () => {
		const markup = html(RESOURCE, 'legacy');

		expect(markup).toContain('<option value="legacy" selected="">legacy</option>');
		expect(markup).toContain('<option value="message">Message</option>');
		expect(markup).toContain('<option value="mailbox">Mailbox</option>');
	});

	it('an options property fed by a loader shows its current value until the loader answers', () => {
		const loaded: PropertyDefinition = {
			key: 'operation',
			label: 'Operation',
			kind: 'options',
			required: false,
			loadOptions: { source: 'node', name: 'operation', dependsOn: ['resource'] }
		};

		const markup = html(loaded, 'sendMessage');

		expect(markup).toContain('<select');
		expect(markup).toContain('<option value="sendMessage" selected="">sendMessage</option>');
	});
});
