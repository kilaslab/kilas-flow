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

describe('a code property', () => {
	const GO_CODE: PropertyDefinition = {
		key: 'code',
		label: 'Go code',
		kind: 'string',
		required: true,
		default: 'return items, nil',
		typeOptions: { rows: 12, editor: 'code', editorLanguage: 'go' }
	};

	it('renders a code editor rather than a one-line input, even for a one-line body', () => {
		// The default is one line, which is exactly the value that used to pick
		// <input type=text>: Enter did nothing and a pasted body lost its newlines.
		const markup = html(GO_CODE, 'return items, nil');

		expect(markup).toContain('data-code-editor="go"');
		expect(markup).not.toContain('<input');
		expect(markup).not.toContain('<textarea');
	});

	it('offers no expression toggle, since a template marker in a program is program text', () => {
		const markup = html(GO_CODE, 'return items, nil');

		expect(markup).not.toContain('role="switch"');
	});

	it('names the editor with a plain label, because a <label for> cannot name a contenteditable', () => {
		const markup = html(GO_CODE, 'return items, nil');

		expect(markup).not.toContain('<label');
		expect(markup).toContain('Go code');
	});

	it('a string with rows but no editor is still a textarea, as a sticky note content is', () => {
		const content: PropertyDefinition = {
			key: 'content',
			label: 'Content',
			kind: 'string',
			required: false,
			default: '## Note',
			typeOptions: { rows: 6 }
		};

		const markup = html(content, '## Note');

		expect(markup).toContain('<textarea');
		expect(markup).not.toContain('data-code-editor');
	});
});

/**
 * A mapper that opts into the model switch — the Workflow Tool's inputs. Each
 * column is either the model's (a `$fromAI` value, or nothing written) or
 * fixed, and the form says which. The columns come from the stored schema copy
 * because `$effect` never runs on the server, which is how a saved node renders
 * before its loader answers.
 */
describe('a resource mapper that lets the model fill a column', () => {
	const columns = [
		{ id: 'tempC', displayName: 'tempC', type: 'number' },
		{ id: 'roundTo', displayName: 'roundTo', type: 'number' },
		{ id: 'unit', displayName: 'unit', type: 'string' },
		{ id: 'verbose', displayName: 'verbose', type: 'boolean' },
		{ id: 'note', displayName: 'note', type: 'string' }
	];
	const mapperProperty = (modelDefinable: boolean): PropertyDefinition => ({
		key: 'workflowInputs',
		label: 'Workflow inputs',
		kind: 'resourceMapper',
		required: false,
		mapper: {
			schema: { source: 'internal', name: 'workflows.inputs', dependsOn: ['workflowId'] },
			valuesLabel: 'Workflow inputs',
			...(modelDefinable ? { modelDefinable: true } : {})
		}
	});
	const stored = {
		mappingMode: 'defineBelow',
		value: {
			tempC: { mode: 'expression', value: "{{ $fromAI('tempC', '', 'number') }}" },
			unit: 'F',
			note: { mode: 'expression', value: '{{ $json.note }}' }
		},
		matchingColumns: [],
		schema: columns
	};

	/** The opening tag of a column's model switch. */
	function switchOf(markup: string, column: string): string {
		return markup.match(new RegExp(`<button[^>]*aria-label="${column}: defined by the model"[^>]*>`))?.[0] ?? '';
	}
	const hasControl = (markup: string, column: string) => markup.includes(`aria-label="${column}"`);

	it('renders one switch per column, on for the model’s and off for a fixed one', () => {
		const markup = html(mapperProperty(true), stored);

		expect(switchOf(markup, 'tempC')).toContain('role="switch"');
		expect(switchOf(markup, 'tempC')).toContain('aria-checked="true"');
		// Nothing written for a column leaves it to the model.
		expect(switchOf(markup, 'roundTo')).toContain('aria-checked="true"');
		expect(switchOf(markup, 'unit')).toContain('aria-checked="false"');
		expect(switchOf(markup, 'note')).toContain('aria-checked="false"');
		expect(switchOf(markup, 'verbose')).toContain('aria-checked="true"');
	});

	it('shows no value control for a column the model fills, and the typed control for a fixed one', () => {
		const markup = html(mapperProperty(true), stored);

		expect(hasControl(markup, 'tempC')).toBe(false);
		expect(hasControl(markup, 'roundTo')).toBe(false);
		expect(markup).toContain('The model fills this in when it calls the tool.');
		expect(hasControl(markup, 'unit')).toBe(true);
		expect(markup).toMatch(/<input[^>]*aria-label="unit"[^>]*value="F"/);
	});

	// The marker's JSON in a text box, and typing over it, destroyed the
	// expression with the first keystroke.
	it('shows a fixed expression column as its template, not as the marker’s JSON', () => {
		const markup = html(mapperProperty(true), stored);

		expect(markup).toMatch(/<input[^>]*aria-label="note"[^>]*value="\{\{ \$json\.note \}\}"/);
		expect(markup).not.toContain('&quot;mode&quot;');
	});

	it('renders the model switch as the editor’s pill, with no colour of its own', () => {
		const markup = html(mapperProperty(true), stored);
		const pill = switchOf(markup, 'tempC');

		expect(pill).toContain('border-border');
		expect(pill).toContain('aria-checked:text-primary');
		expect(pill).not.toMatch(/outline-none|outline-hidden|focus-visible:/);
		expect(pill).not.toMatch(/#[0-9a-f]{3,6}|rgb\(|(?:red|green|blue|amber|emerald|sky)-\d/);
	});

	it('does not wrap the switch and the control in one label, so a click on the name cannot flip the switch', () => {
		const markup = html(mapperProperty(true), stored);

		expect(markup).not.toMatch(/<label[^>]*>(?:(?!<\/label>)[\s\S])*role="switch"/);
	});

	// Track-neutral guard: a mapper that never asked for the switch (a table's
	// columns) has no business offering a column to a model.
	it('renders no switch at all for a mapper that did not opt in', () => {
		const markup = html(mapperProperty(false), stored);

		expect(markup).not.toContain('role="switch"');
		expect(markup).not.toContain('defined by the model');
		expect(hasControl(markup, 'tempC')).toBe(true);
		expect(hasControl(markup, 'roundTo')).toBe(true);
	});

	it('reads a plain object stored before the property was a mapper as fixed columns', () => {
		const markup = html(mapperProperty(true), { unit: 'F' });

		// No schema copy yet, so there is no column to switch; the stored key
		// must not have been wiped from view, and nothing claims the model has it.
		expect(markup).not.toContain('role="switch"');
	});

	it('lists a stored key the workflow does not declare, so a legacy static input stays visible', () => {
		const markup = html(mapperProperty(true), { ...stored, value: { ...stored.value, extra: 'x' } });

		expect(markup).toContain('Also sent, though the workflow does not declare them:');
		expect(markup).toContain('>extra');
		expect(markup).toContain('Remove extra');
		// The declared columns are not in that list.
		expect(markup).not.toContain('aria-label="Remove unit"');
	});

	it('says so, with a way out, when the inputs are one expression', () => {
		const markup = html(mapperProperty(true), { mode: 'expression', value: '{{ { unit: "F" } }}' });

		expect(markup).toContain('These inputs are written as one expression');
		expect(markup).toContain('Use per-field inputs');
		expect(markup).not.toContain('role="switch"');
	});
});
