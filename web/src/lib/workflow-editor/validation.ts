import type { Node } from '$lib/api/generated/models';

export type CanvasValidationIssue = {
	message: string;
	code?: string;
	nodeID?: string;
	connectionID?: string;
};

type ProblemError = {
	message?: unknown;
	value?: unknown;
};

type ApiProblemError = {
	status?: unknown;
	problem?: { errors?: unknown };
};

/**
 * Turns the structured compiler problem returned by the API into canvas
 * locations. Keeping this at the HTTP boundary lets the editor stay generic:
 * it never needs node-type-specific validation rules.
 */
export function validationIssuesFromApiError(error: unknown): CanvasValidationIssue[] {
	if (!isApiProblemError(error) || error.status !== 422 || !Array.isArray(error.problem?.errors)) return [];

	return error.problem.errors.flatMap((detail) => {
		if (!isProblemError(detail) || typeof detail.message !== 'string' || detail.message === '') return [];
		const issue: CanvasValidationIssue = { message: detail.message };
		if (isIssueValue(detail.value)) {
			if (typeof detail.value.code === 'string') issue.code = detail.value.code;
			if (typeof detail.value.nodeId === 'string') issue.nodeID = detail.value.nodeId;
			if (typeof detail.value.connectionId === 'string') issue.connectionID = detail.value.connectionId;
		}
		// An issue about the workflow itself — no node, no connection, and for a
		// refused document no structured value at all — is still an issue.
		// Dropping it left the editor with nothing to show for a refusal the user
		// cannot see anywhere else.
		return [issue];
	});
}

/**
 * Names the nodes an issue is about, where the server could only name an id.
 *
 * The compiler writes `node "ef4c6982-…" configuration is invalid…`, and a UUID
 * is not something a user can match to a tile on the canvas. The id is kept
 * when no node resolves it, because an unrecognised id is still evidence.
 */
export function withNodeNames(
	issues: CanvasValidationIssue[],
	nodes: Pick<Node, 'id' | 'name'>[] | null | undefined
): CanvasValidationIssue[] {
	if (!nodes || nodes.length === 0) return issues;
	const nameByID = new Map(nodes.filter((node) => node.id).map((node) => [node.id, node.name]));
	return issues.map((issue) => {
		const name = issue.nodeID ? nameByID.get(issue.nodeID) : undefined;
		if (!name) return issue;
		return { ...issue, message: replaceStandaloneID(issue.message, issue.nodeID!, name) };
	});
}

/**
 * Replaces an id only where it stands alone in the message: not preceded or
 * followed by a character an id or a word is made of. A node id is whatever the
 * author typed (`call` is one), so a plain substring replacement also rewrote
 * the `call` in `calls workflow` and in another node's name.
 */
function replaceStandaloneID(message: string, id: string, name: string): string {
	const escaped = id.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
	// A function replacement, so a `$&` or `$1` in the node's name stays text.
	return message.replace(new RegExp(`(?<![\\w.-])${escaped}(?![\\w.-])`, 'g'), () => name);
}

function isApiProblemError(value: unknown): value is ApiProblemError {
	return value !== null && typeof value === 'object';
}

function isProblemError(value: unknown): value is ProblemError {
	return value !== null && typeof value === 'object';
}

function isIssueValue(value: unknown): value is Record<string, unknown> {
	return value !== null && typeof value === 'object' && !Array.isArray(value);
}
