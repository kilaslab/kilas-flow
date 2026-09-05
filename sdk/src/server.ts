/**
 * Server-side KilasFlow client.
 *
 * This module is for a host's backend. It carries the host's API credentials,
 * so it must never be bundled into a browser — which is why embed-session
 * creation lives here and iframe mounting lives in `./browser`, with no import
 * path from the browser entry point back to this one.
 */

import { Transport, type TransportOptions } from './http.js';
import type {
	CredentialResource,
	EmbedSessionResource,
	ExecutionListResource,
	ExecutionResource,
	ScheduleResource,
	WorkflowDocumentInput,
	WorkflowResource,
	WorkflowSummary,
	WorkflowVersionResource
} from './generated/models.js';

export type { TransportOptions } from './http.js';
export { KilasFlowError } from './http.js';

/** Scopes an embed session may carry. Write and run both imply read. */
export type EmbedScope = 'workflow:read' | 'workflow:write' | 'workflow:run';

export interface EmbedSessionRequest {
	workflowId: string;
	scopes: EmbedScope[];
	/** Exact origin of the page that will frame the editor. No wildcards. */
	origin: string;
	ttlSeconds?: number;
	branding?: {
		name?: string;
		logoUrl?: string;
		accent?: string;
		hideRun?: boolean;
		hideSave?: boolean;
	};
}

export interface ExecutionListQuery {
	workflowId?: string;
	status?: string[];
	trigger?: string;
	limit?: number;
	cursor?: string;
}

/**
 * The host-facing client.
 *
 * Every method is a thin, typed call onto a documented endpoint. The SDK is a
 * convenience layer, never an alternate source of state or authorization: it
 * holds no cache, makes no decisions the server would not make, and adds no
 * permission the API does not already grant.
 */
export class KilasFlowClient {
	readonly #transport: Transport;

	constructor(options: TransportOptions) {
		this.#transport = new Transport(options);
	}

	/** Base URL this client talks to, useful for building embed URLs. */
	get baseUrl(): string {
		return this.#transport.baseUrl;
	}

	// --- Workflows -----------------------------------------------------------

	listWorkflows(signal?: AbortSignal): Promise<WorkflowSummary[]> {
		return this.#transport.request('GET', '/workflows', { signal });
	}

	getWorkflow(workflowId: string, signal?: AbortSignal): Promise<WorkflowResource> {
		return this.#transport.request('GET', `/workflows/${encodeURIComponent(workflowId)}`, { signal });
	}

	createWorkflow(document: WorkflowDocumentInput, signal?: AbortSignal): Promise<WorkflowResource> {
		return this.#transport.request('POST', '/workflows', { body: document, signal });
	}

	updateWorkflow(workflowId: string, document: WorkflowDocumentInput, signal?: AbortSignal): Promise<WorkflowResource> {
		return this.#transport.request('PUT', `/workflows/${encodeURIComponent(workflowId)}`, { body: document, signal });
	}

	deleteWorkflow(workflowId: string, signal?: AbortSignal): Promise<void> {
		return this.#transport.request('DELETE', `/workflows/${encodeURIComponent(workflowId)}`, { signal });
	}

	/** Reads one immutable revision, such as the one an execution pinned. */
	getWorkflowVersion(workflowId: string, versionId: string, signal?: AbortSignal): Promise<WorkflowVersionResource> {
		return this.#transport.request(
			'GET',
			`/workflows/${encodeURIComponent(workflowId)}/versions/${encodeURIComponent(versionId)}`,
			{ signal }
		);
	}

	activateWorkflow(workflowId: string, signal?: AbortSignal): Promise<WorkflowResource> {
		return this.#transport.request('POST', `/workflows/${encodeURIComponent(workflowId)}/activate`, { signal });
	}

	deactivateWorkflow(workflowId: string, signal?: AbortSignal): Promise<WorkflowResource> {
		return this.#transport.request('POST', `/workflows/${encodeURIComponent(workflowId)}/deactivate`, { signal });
	}

	/** Queues a manual run and returns immediately with the execution record. */
	runWorkflow(workflowId: string, input?: unknown, signal?: AbortSignal): Promise<{ id: string; status: string }> {
		return this.#transport.request('POST', `/workflows/${encodeURIComponent(workflowId)}/run`, {
			body: input === undefined ? {} : { input },
			signal
		});
	}

	// --- Executions ----------------------------------------------------------

	listExecutions(query: ExecutionListQuery = {}, signal?: AbortSignal): Promise<ExecutionListResource> {
		return this.#transport.request('GET', '/executions', { query: { ...query }, signal });
	}

	getExecution(executionId: string, signal?: AbortSignal): Promise<ExecutionResource> {
		return this.#transport.request('GET', `/executions/${encodeURIComponent(executionId)}`, { signal });
	}

	cancelExecution(executionId: string, signal?: AbortSignal): Promise<{ id: string; status: string }> {
		return this.#transport.request('POST', `/executions/${encodeURIComponent(executionId)}/cancel`, { signal });
	}

	/** Absolute URL of an execution's live event stream. */
	executionEventsUrl(executionId: string): string {
		return this.#transport.url(`/executions/${encodeURIComponent(executionId)}/events`);
	}

	// --- Supporting resources ------------------------------------------------

	listCredentials(signal?: AbortSignal): Promise<CredentialResource[]> {
		return this.#transport.request('GET', '/credentials', { signal });
	}

	listSchedules(signal?: AbortSignal): Promise<ScheduleResource[]> {
		return this.#transport.request('GET', '/schedules', { signal });
	}

	// --- Embedding -----------------------------------------------------------

	/**
	 * Mints a short-lived, workflow-scoped session for one host origin.
	 *
	 * This is a backend call because it uses the host's own credentials. The
	 * resulting token is what the browser receives — never the API key.
	 */
	// Async so a validation failure rejects rather than throwing
	// synchronously: a caller awaits this, and a sync throw would escape their
	// .catch() and surface as an unhandled error instead.
	async createEmbedSession(request: EmbedSessionRequest, signal?: AbortSignal): Promise<EmbedSessionResource> {
		if (!request.workflowId) throw new Error('createEmbedSession needs a workflowId');
		if (!request.origin) throw new Error('createEmbedSession needs the exact origin that will frame the editor');
		if (!request.scopes?.length) throw new Error('createEmbedSession needs at least one scope');
		return this.#transport.request('POST', '/embed-sessions', { body: request, signal });
	}
}
