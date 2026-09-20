import type { QueryClient } from '@tanstack/svelte-query';

import { getListWorkflowsQueryKey, getGetWorkflowQueryKey } from '$lib/api/generated/workflows/workflows';
import type { WorkflowResource } from '$lib/api/generated/models';

/**
 * The shape `apiFetch` answers with.
 *
 * A generated query's `select` reads the envelope rather than the workflow
 * itself, so a mutation that wants to place its answer in the cache has to
 * rebuild one. Writing the bare workflow instead would make the next mount of
 * the editor throw on `response.status`.
 */
export type WorkflowQueryEnvelope = { status: number; data: WorkflowResource; headers: Headers };

/**
 * Publishes a mutation's answer into the shared cache.
 *
 * The page keeps its own copy of the workflow, and the page is not what the
 * next mount reads: the cache is. Without this, saving and then reopening the
 * workflow within the query's 30-second staleness window mounted the editor
 * from the revision *before* the save, and the save after that reverted it —
 * the user's own work overwritten by their own stale copy.
 *
 * The list is invalidated rather than patched: its rows show a revision and an
 * active flag that a save or an activation both move, and it is a cheap
 * background refetch.
 */
export function cacheWorkflow(queryClient: QueryClient, workflow: WorkflowResource): void {
	const envelope: WorkflowQueryEnvelope = { status: 200, data: workflow, headers: new Headers() };
	queryClient.setQueryData<WorkflowQueryEnvelope>(getGetWorkflowQueryKey(workflow.id), envelope);
	void queryClient.invalidateQueries({ queryKey: getListWorkflowsQueryKey() });
}
