import { describe, expect, it } from 'vitest';

import { ApiError } from '$lib/api/http';

import { deleteParams, isInUseConflict, usageOf } from './credential-usage';

describe('which workflows a credential is used by', () => {
	it('reads usedBy from a single read, in the order the server gave', () => {
		expect(
			usageOf({
				usedBy: [
					{ workflowId: 'wf_b', name: 'Billing inbox', active: true, nodes: ['Inbox watcher'] },
					{ workflowId: 'wf_a', name: 'Support', active: false, nodes: null }
				]
			})
		).toEqual([
			{ workflowId: 'wf_b', name: 'Billing inbox', active: true, nodes: ['Inbox watcher'] },
			{ workflowId: 'wf_a', name: 'Support', active: false, nodes: [] }
		]);
	});

	// The server leaves usedBy out when nothing uses the credential.
	it('reads an absent or null usedBy as no workflows', () => {
		expect(usageOf({})).toEqual([]);
		expect(usageOf({ usedBy: null })).toEqual([]);
		expect(usageOf(null)).toEqual([]);
	});
});

describe('how the delete is sent', () => {
	// Force is only sent once the dialog has shown the workflows it will break,
	// so a delete confirmed against an empty list is refused if one appeared.
	it('forces only a delete whose dialog listed workflows', () => {
		expect(deleteParams([])).toBeUndefined();
		expect(deleteParams([{ workflowId: 'wf_a', name: 'Support', active: false, nodes: [] }])).toEqual({
			force: true
		});
	});

	it('recognises the in-use refusal and nothing else', () => {
		expect(isInUseConflict(new ApiError(409, 'this credential is attached by 1 workflow'))).toBe(true);
		expect(isInUseConflict(new ApiError(404, 'credential not found'))).toBe(false);
		expect(isInUseConflict(new Error('network down'))).toBe(false);
		expect(isInUseConflict(undefined)).toBe(false);
	});
});
