import { describe, expect, it } from 'vitest';

import type { Document } from '$lib/api/generated/models';

import {
	applySettingsForm,
	errorWorkflowOptions,
	readSettingsForm,
	renameWorkflow,
	timezoneOptions,
	type WorkflowSettingsForm
} from './workflow-settings';

function documentWith(settings: Record<string, unknown> = {}, name = 'Orders'): Document {
	return { id: 'wf_test', schemaVersion: 1, name, nodes: [], connections: [], settings } as Document;
}

const empty: WorkflowSettingsForm = { timeoutSeconds: '', noTimeout: false, errorWorkflow: '', timezone: '' };

function applied(document: Document, form: WorkflowSettingsForm): Document {
	const result = applySettingsForm(document, form);
	if ('problem' in result) throw new Error(`unexpected problem ${result.problem}`);
	return result.document;
}

describe('readSettingsForm', () => {
	it('reads an empty form from a document with no settings', () => {
		expect(readSettingsForm(documentWith())).toEqual(empty);
	});

	it('reads the three keys the engine honours', () => {
		const form = readSettingsForm(documentWith({ executionTimeout: 90, errorWorkflow: 'wf_alerts', timezone: 'Asia/Jakarta' }));
		expect(form).toEqual({ timeoutSeconds: '90', noTimeout: false, errorWorkflow: 'wf_alerts', timezone: 'Asia/Jakarta' });
	});

	it('reads a negative timeout as "no time limit"', () => {
		expect(readSettingsForm(documentWith({ executionTimeout: -1 }))).toMatchObject({ noTimeout: true, timeoutSeconds: '' });
	});

	it("reads n8n's DEFAULT zone as the server default", () => {
		expect(readSettingsForm(documentWith({ timezone: 'DEFAULT' })).timezone).toBe('');
	});

	it('ignores values of the wrong type instead of showing them', () => {
		expect(readSettingsForm(documentWith({ executionTimeout: '90', errorWorkflow: 3, timezone: 7 }))).toEqual(empty);
	});
});

describe('applySettingsForm', () => {
	it('writes the keys under the names the engine reads', () => {
		const next = applied(documentWith(), { timeoutSeconds: '120', noTimeout: false, errorWorkflow: ' wf_alerts ', timezone: 'Europe/Paris' });
		expect(next.settings).toEqual({ executionTimeout: 120, errorWorkflow: 'wf_alerts', timezone: 'Europe/Paris' });
	});

	it('writes -1 for no time limit, whatever the seconds field says', () => {
		const next = applied(documentWith(), { ...empty, noTimeout: true, timeoutSeconds: '30' });
		expect(next.settings).toEqual({ executionTimeout: -1 });
	});

	it('removes a key whose field was emptied', () => {
		const start = documentWith({ executionTimeout: 60, errorWorkflow: 'wf_a', timezone: 'UTC' });
		expect(applied(start, empty).settings).toEqual({});
	});

	it('keeps settings the dialog does not own', () => {
		const start = documentWith({ executionOrder: 'v1', timezone: 'UTC' });
		expect(applied(start, { ...empty, timezone: 'Asia/Tokyo' }).settings).toEqual({ executionOrder: 'v1', timezone: 'Asia/Tokyo' });
	});

	it('does not mutate the document it was given', () => {
		const start = documentWith({ timezone: 'UTC' });
		applied(start, empty);
		expect(start.settings).toEqual({ timezone: 'UTC' });
	});

	it('returns the same document when nothing changed, so the draft stays clean', () => {
		const start = documentWith({ executionTimeout: 60, errorWorkflow: 'wf_a', timezone: 'UTC' });
		expect(applied(start, readSettingsForm(start))).toBe(start);
		const none = documentWith();
		expect(applied(none, empty)).toBe(none);
	});

	it.each(['0', '-5', 'abc', '1e999'])('refuses %s as a timeout', (value) => {
		expect(applySettingsForm(documentWith(), { ...empty, timeoutSeconds: value })).toEqual({ problem: 'timeout' });
	});

	it('accepts a fractional number of seconds, which the engine reads as a float', () => {
		expect(applied(documentWith(), { ...empty, timeoutSeconds: '1.5' }).settings).toEqual({ executionTimeout: 1.5 });
	});
});

describe('renameWorkflow', () => {
	it('trims the name', () => {
		const result = renameWorkflow(documentWith(), '  Invoices  ');
		expect(result).toEqual({ document: expect.objectContaining({ name: 'Invoices' }) });
	});

	it('returns the same document when the name did not change', () => {
		const start = documentWith();
		expect((renameWorkflow(start, ' Orders ') as { document: Document }).document).toBe(start);
	});

	it('refuses an empty name and one past the API limit', () => {
		expect(renameWorkflow(documentWith(), '   ')).toEqual({ problem: 'required' });
		expect(renameWorkflow(documentWith(), 'x'.repeat(256))).toEqual({ problem: 'too_long' });
		expect('problem' in renameWorkflow(documentWith(), 'x'.repeat(255))).toBe(false);
	});
});

describe('timezoneOptions', () => {
	it('adds UTC and the stored zone when the runtime does not list them', () => {
		expect(timezoneOptions(['Asia/Jakarta'], 'Custom/Zone')).toEqual(['Asia/Jakarta', 'Custom/Zone', 'UTC']);
	});

	it('lists each zone once, sorted', () => {
		expect(timezoneOptions(['UTC', 'Europe/Paris', 'Asia/Jakarta'], 'UTC')).toEqual(['Asia/Jakarta', 'Europe/Paris', 'UTC']);
	});
});

describe('errorWorkflowOptions', () => {
	const workflows = [
		{ id: 'wf_b', name: 'Beta' },
		{ id: 'wf_self', name: 'This one' },
		{ id: 'wf_a', name: 'Alpha' }
	];

	it('offers every other workflow by name and never the current one', () => {
		expect(errorWorkflowOptions(workflows, 'wf_self', '').map((option) => option.id)).toEqual(['wf_a', 'wf_b']);
	});

	it('keeps a stored id the tenant no longer lists, marked missing', () => {
		const options = errorWorkflowOptions(workflows, 'wf_self', 'wf_gone');
		expect(options[0]).toEqual({ id: 'wf_gone', name: 'wf_gone', missing: true });
		expect(options.slice(1).every((option) => !option.missing)).toBe(true);
	});

	it('does not mark a listed selection as missing', () => {
		expect(errorWorkflowOptions(workflows, 'wf_self', 'wf_a').some((option) => option.missing)).toBe(false);
	});
});
