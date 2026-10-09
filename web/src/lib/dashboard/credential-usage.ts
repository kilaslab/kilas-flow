import type { CredentialResource, DeleteCredentialParams } from '$lib/api/generated/models';
import { ApiError } from '$lib/api/http';

/** One workflow that attaches a credential, as the delete dialog lists it. */
export interface CredentialUsage {
	workflowId: string;
	name: string;
	active: boolean;
	nodes: string[];
}

/**
 * The workflows a single credential read says use it. The server leaves
 * usedBy out when nothing does, so absent means none.
 */
export function usageOf(resource: Pick<CredentialResource, 'usedBy'> | null | undefined): CredentialUsage[] {
	return (resource?.usedBy ?? []).map((user) => ({
		workflowId: user.workflowId,
		name: user.name,
		active: user.active,
		nodes: user.nodes ?? []
	}));
}

/**
 * The query the delete is sent with. Force goes only with a delete whose
 * dialog listed the workflows it breaks: one confirmed against an empty list
 * is refused by the server if a workflow attached the credential meanwhile,
 * and the dialog then shows that workflow instead of deleting past it.
 */
export function deleteParams(shown: CredentialUsage[]): DeleteCredentialParams | undefined {
	return shown.length > 0 ? { force: true } : undefined;
}

/** Whether a delete was refused because workflows still use the credential. */
export function isInUseConflict(error: unknown): boolean {
	return error instanceof ApiError && error.status === 409;
}
