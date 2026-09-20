import { QueryClient } from '@tanstack/svelte-query';
import { describe, expect, it } from 'vitest';

import type { WorkflowResource } from '$lib/api/generated/models';
import { getGetWorkflowQueryKey, getListWorkflowsQueryKey } from '$lib/api/generated/workflows/workflows';
import { cacheWorkflow, type WorkflowQueryEnvelope } from './workflow-cache';

function workflow(revision: number, name: string): WorkflowResource {
	return {
		id: 'wf-1',
		name,
		active: false,
		latestVersion: {
			id: `ver-${revision}`,
			revision,
			document: { schemaVersion: 1, name, nodes: [], connections: [] }
		}
	} as unknown as WorkflowResource;
}

describe('publishing a mutation answer into the query cache', () => {
	it('replaces the revision a reopened editor would mount from', () => {
		const client = new QueryClient();
		const stale: WorkflowQueryEnvelope = { status: 200, data: workflow(3, 'Before'), headers: new Headers() };
		client.setQueryData(getGetWorkflowQueryKey('wf-1'), stale);

		cacheWorkflow(client, workflow(4, 'After'));

		// The envelope, not the bare workflow: the editor's `select` reads
		// `status` off it and throws when it is missing.
		const cached = client.getQueryData<WorkflowQueryEnvelope>(getGetWorkflowQueryKey('wf-1'));
		expect(cached?.status).toBe(200);
		expect(cached?.data.latestVersion.revision).toBe(4);
		expect(cached?.data.latestVersion.document.name).toBe('After');
	});

	it('refreshes the list, whose rows carry the revision and the active flag', () => {
		const client = new QueryClient();
		client.setQueryData(getListWorkflowsQueryKey(), { status: 200, data: [], headers: new Headers() });
		expect(client.getQueryState(getListWorkflowsQueryKey())?.isInvalidated).toBe(false);

		cacheWorkflow(client, workflow(2, 'Saved'));

		expect(client.getQueryState(getListWorkflowsQueryKey())?.isInvalidated).toBe(true);
	});
});
