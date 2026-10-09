import { describe, expect, it } from 'vitest';

import type { Node } from '$lib/api/generated/models';
import { validationIssuesFromApiError, withNodeNames } from './validation';

describe('workflow validation feedback', () => {
	it('extracts compiler issue locations for the corresponding canvas elements', () => {
		const issues = validationIssuesFromApiError({
			status: 422,
			problem: {
				title: 'Unprocessable Entity',
				status: 422,
				detail: 'workflow validation failed',
				errors: [
					{ message: 'Set needs an input', value: { code: 'workflow.required_input', nodeId: 'set-1' } },
					{ message: 'Ports cannot be connected', value: { code: 'workflow.incompatible_port', connectionId: 'edge-1' } },
					{ message: 'Workflow has a cycle', value: { code: 'workflow.cycle' } }
				]
			}
		});

		expect(issues).toEqual([
			{ message: 'Set needs an input', code: 'workflow.required_input', nodeID: 'set-1' },
			{ message: 'Ports cannot be connected', code: 'workflow.incompatible_port', connectionID: 'edge-1' },
			{ message: 'Workflow has a cycle', code: 'workflow.cycle' }
		]);
	});

	// A refused document carries the reason in the message alone. Throwing it
	// away left the editor showing a bare "422 — workflow draft is invalid" and
	// an empty issue list, which is the whole complaint this covers.
	it('keeps an issue the server could not locate anywhere', () => {
		expect(
			validationIssuesFromApiError({
				status: 422,
				problem: { title: 'Unprocessable Entity', status: 422, detail: 'workflow draft is invalid', errors: [{ message: 'schemaVersion 2 is not supported', location: 'body' }] }
			})
		).toEqual([{ message: 'schemaVersion 2 is not supported' }]);
	});

	it('ignores non-validation failures and malformed problem details', () => {
		expect(validationIssuesFromApiError({ status: 500, problem: undefined })).toEqual([]);
		expect(validationIssuesFromApiError({ status: 422, problem: { title: 'bad', status: 422, errors: [{ message: '', value: 'nope' }] } })).toEqual([]);
	});
});

describe('naming the nodes an issue is about', () => {
	const nodes = [
		{ id: 'ef4c6982-0000-0000-0000-000000000000', name: 'Send to Slack' },
		{ id: 'aaaa1111-0000-0000-0000-000000000000', name: 'Fetch' }
	] as unknown as Node[];

	it('replaces the id the compiler wrote with the name the canvas shows', () => {
		expect(
			withNodeNames([{ message: 'node "ef4c6982-0000-0000-0000-000000000000" configuration is invalid', nodeID: 'ef4c6982-0000-0000-0000-000000000000' }], nodes)
		).toEqual([{ message: 'node "Send to Slack" configuration is invalid', nodeID: 'ef4c6982-0000-0000-0000-000000000000' }]);
	});

	it('replaces the id only where it stands alone, never inside a longer word', () => {
		// An id such as `call` is a legitimate node id, and the activation refusal
		// for a sub-workflow call says `calls workflow` and names `Run billings`
		// in the same sentence.
		const message = 'node "Run billings" (call) calls workflow "Billing" (wf_1), which is not active';
		expect(withNodeNames([{ message, nodeID: 'call' }], [{ id: 'call', name: 'Run billings' }] as unknown as Node[])).toEqual([
			{ message: 'node "Run billings" (Run billings) calls workflow "Billing" (wf_1), which is not active', nodeID: 'call' }
		]);
	});

	it('does not treat a longer id that contains this one as a match', () => {
		const message = 'node "ab1" and node "ab12" are both wrong';
		expect(withNodeNames([{ message, nodeID: 'ab1' }], [{ id: 'ab1', name: 'First' }] as unknown as Node[])).toEqual([
			{ message: 'node "First" and node "ab12" are both wrong', nodeID: 'ab1' }
		]);
	});

	it('reads an id that carries regular expression characters literally', () => {
		const message = 'node "a.b" calls "axb"';
		expect(withNodeNames([{ message, nodeID: 'a.b' }], [{ id: 'a.b', name: 'Dotted' }] as unknown as Node[])).toEqual([
			{ message: 'node "Dotted" calls "axb"', nodeID: 'a.b' }
		]);
	});

	it('leaves an issue alone when no node answers to the id', () => {
		const issue = { message: 'node "ffff" is unknown', nodeID: 'ffff' };
		expect(withNodeNames([issue], nodes)).toEqual([issue]);
		expect(withNodeNames([issue], null)).toEqual([issue]);
	});
});
