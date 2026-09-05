import { describe, expect, it } from 'vitest';

import { validationIssuesFromApiError } from './validation';

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
					{ message: 'Unmapped validation detail' }
				]
			}
		});

		expect(issues).toEqual([
			{ message: 'Set needs an input', code: 'workflow.required_input', nodeID: 'set-1' },
			{ message: 'Ports cannot be connected', code: 'workflow.incompatible_port', connectionID: 'edge-1' }
		]);
	});

	it('ignores non-validation failures and malformed problem details', () => {
		expect(validationIssuesFromApiError({ status: 500, problem: undefined })).toEqual([]);
		expect(validationIssuesFromApiError({ status: 422, problem: { title: 'bad', status: 422, errors: [{ message: 'bad', value: 'nope' }] } })).toEqual([]);
	});
});
