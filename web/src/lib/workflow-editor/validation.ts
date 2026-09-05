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
		if (!isProblemError(detail) || !isIssueValue(detail.value)) return [];
		const message = typeof detail.message === 'string' ? detail.message : 'Workflow validation failed.';
		const issue: CanvasValidationIssue = { message };
		if (typeof detail.value.code === 'string') issue.code = detail.value.code;
		if (typeof detail.value.nodeId === 'string') issue.nodeID = detail.value.nodeId;
		if (typeof detail.value.connectionId === 'string') issue.connectionID = detail.value.connectionId;
		return issue.nodeID || issue.connectionID ? [issue] : [];
	});
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
