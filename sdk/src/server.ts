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
	APIKeyResource,
	CreatedAPIKeyResource,
	CredentialBody,
	CredentialResource,
	CredentialTypeResource,
	Definition,
	EmbedSessionResource,
	ExecutionListResource,
	ExecutionResource,
	ExportedWorkflowResource,
	ExpressionGrammar,
	GetNodeIconTheme,
	HealthOutputBody,
	ImportedWorkflowResource,
	ImportWorkflowInputBody,
	ListAPIKeysOutputBody,
	LoadOptionsInputBody,
	LoadOptionsResource,
	LoadSchemaResource,
	LoginInputBody,
	PrincipalResource,
	ReadyOutputBody,
	ScheduleBody,
	ScheduleResource,
	StreamTicketResource,
	TestCredentialResource,
	TestPayloadBody,
	WorkflowDocumentInput,
	WorkflowPublishEventResource,
	WorkflowResource,
	WorkflowSummary,
	WorkflowVersionListResource,
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

export interface WorkflowVersionListQuery {
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

	/** Lists one workflow's revisions, newest first. The rows omit the document. */
	listWorkflowVersions(
		workflowId: string,
		query: WorkflowVersionListQuery = {},
		signal?: AbortSignal
	): Promise<WorkflowVersionListResource> {
		return this.#transport.request('GET', `/workflows/${encodeURIComponent(workflowId)}/versions`, {
			query: { ...query },
			signal
		});
	}

	/**
	 * Publishes a revision so production traffic runs it. The reason is
	 * optional and lands in the publish audit trail when given.
	 */
	publishWorkflowVersion(
		workflowId: string,
		versionId: string,
		reason?: string,
		signal?: AbortSignal
	): Promise<WorkflowResource> {
		return this.#transport.request(
			'POST',
			`/workflows/${encodeURIComponent(workflowId)}/versions/${encodeURIComponent(versionId)}/publish`,
			{ body: reason === undefined ? undefined : { reason }, signal }
		);
	}

	/** Restores a revision's document into a new draft. Shares publish's audit reason. */
	restoreWorkflowVersion(
		workflowId: string,
		versionId: string,
		reason?: string,
		signal?: AbortSignal
	): Promise<WorkflowResource> {
		return this.#transport.request(
			'POST',
			`/workflows/${encodeURIComponent(workflowId)}/versions/${encodeURIComponent(versionId)}/restore`,
			{ body: reason === undefined ? undefined : { reason }, signal }
		);
	}

	/** Reads a workflow's publish audit trail: what was published, unpublished, or restored. */
	listWorkflowPublishEvents(workflowId: string, signal?: AbortSignal): Promise<WorkflowPublishEventResource[]> {
		return this.#transport.request('GET', `/workflows/${encodeURIComponent(workflowId)}/publish-events`, { signal });
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

	// --- Credentials -----------------------------------------------------------

	/** Lists the credential types a host can render a form for. Values are never returned. */
	listCredentialTypes(signal?: AbortSignal): Promise<CredentialTypeResource[]> {
		return this.#transport.request('GET', '/credential-types', { signal });
	}

	listCredentials(signal?: AbortSignal): Promise<CredentialResource[]> {
		return this.#transport.request('GET', '/credentials', { signal });
	}

	createCredential(body: CredentialBody, signal?: AbortSignal): Promise<CredentialResource> {
		return this.#transport.request('POST', '/credentials', { body, signal });
	}

	getCredential(credentialId: string, signal?: AbortSignal): Promise<CredentialResource> {
		return this.#transport.request('GET', `/credentials/${encodeURIComponent(credentialId)}`, { signal });
	}

	updateCredential(credentialId: string, body: CredentialBody, signal?: AbortSignal): Promise<CredentialResource> {
		return this.#transport.request('PUT', `/credentials/${encodeURIComponent(credentialId)}`, { body, signal });
	}

	deleteCredential(credentialId: string, signal?: AbortSignal): Promise<void> {
		return this.#transport.request('DELETE', `/credentials/${encodeURIComponent(credentialId)}`, { signal });
	}

	/**
	 * Probes a stored credential and reports whether it actually works. The
	 * answer carries no secret and no remote body — only whether the probe
	 * succeeded and, on failure, why.
	 */
	testCredential(credentialId: string, signal?: AbortSignal): Promise<TestCredentialResource> {
		return this.#transport.request('POST', `/credentials/${encodeURIComponent(credentialId)}/test`, { signal });
	}

	/** Probes an unsaved credential payload, e.g. to validate a form before storing it. */
	testCredentialPayload(type: string, body: TestPayloadBody, signal?: AbortSignal): Promise<TestCredentialResource> {
		return this.#transport.request('POST', `/credential-types/${encodeURIComponent(type)}/test`, { body, signal });
	}

	// --- Authentication and keys -----------------------------------------------

	/**
	 * Signs in with email and password. The session arrives as an HttpOnly
	 * cookie in the transport's response headers — the JSON body returned here
	 * only describes the caller it authenticated as.
	 */
	login(credentials: LoginInputBody, signal?: AbortSignal): Promise<PrincipalResource> {
		return this.#transport.request('POST', '/auth/login', { body: credentials, signal });
	}

	logout(signal?: AbortSignal): Promise<void> {
		return this.#transport.request('POST', '/auth/logout', { signal });
	}

	/** Describes the tenant and identity this request authenticated as. */
	getMe(signal?: AbortSignal): Promise<PrincipalResource> {
		return this.#transport.request('GET', '/auth/me', { signal });
	}

	/** Lists this tenant's keys. No secret is ever included. */
	listApiKeys(signal?: AbortSignal): Promise<ListAPIKeysOutputBody> {
		return this.#transport.request('GET', '/api-keys', { signal });
	}

	/**
	 * Mints a key and returns it in full exactly once. The server keeps only a
	 * hash and cannot show it again, so the caller must persist `token` now.
	 */
	createApiKey(label: string, signal?: AbortSignal): Promise<CreatedAPIKeyResource> {
		return this.#transport.request('POST', '/api-keys', { body: { label }, signal });
	}

	/** Revokes a key immediately and permanently, keeping the row for audit. */
	revokeApiKey(keyId: string, signal?: AbortSignal): Promise<APIKeyResource> {
		return this.#transport.request('DELETE', `/api-keys/${encodeURIComponent(keyId)}`, { signal });
	}

	/**
	 * Mints a single-use ticket for one execution's event stream. EventSource
	 * cannot send an Authorization header, so the ticket is spent as a query
	 * parameter on the URL {@link executionEventsUrl} builds.
	 */
	createStreamTicket(executionId: string, signal?: AbortSignal): Promise<StreamTicketResource> {
		return this.#transport.request('POST', '/stream-tickets', { body: { executionId }, signal });
	}

	// --- Schedules -------------------------------------------------------------

	listSchedules(signal?: AbortSignal): Promise<ScheduleResource[]> {
		return this.#transport.request('GET', '/schedules', { signal });
	}

	createSchedule(schedule: ScheduleBody, signal?: AbortSignal): Promise<ScheduleResource> {
		return this.#transport.request('POST', '/schedules', { body: schedule, signal });
	}

	updateSchedule(scheduleId: string, schedule: ScheduleBody, signal?: AbortSignal): Promise<ScheduleResource> {
		return this.#transport.request('PUT', `/schedules/${encodeURIComponent(scheduleId)}`, { body: schedule, signal });
	}

	deleteSchedule(scheduleId: string, signal?: AbortSignal): Promise<void> {
		return this.#transport.request('DELETE', `/schedules/${encodeURIComponent(scheduleId)}`, { signal });
	}

	// --- Node catalogue ----------------------------------------------------------

	/** Lists the registered node types. Consumers must ignore unknown fields on this payload. */
	listNodeTypes(signal?: AbortSignal): Promise<Definition[]> {
		return this.#transport.request('GET', '/node-types', { signal });
	}

	/**
	 * Absolute URL of a node's artwork, for an `<img src>`. Like
	 * {@link executionEventsUrl} this returns a URL rather than bytes: the
	 * route serves an image with an inert content policy and a long immutable
	 * cache lifetime, not JSON, so it does not fit the JSON transport.
	 */
	nodeIconUrl(nodeType: string, options: { version?: string; theme?: GetNodeIconTheme } = {}): string {
		return this.#transport.url(`/node-types/${encodeURIComponent(nodeType)}/icon`, {
			version: options.version,
			theme: options.theme
		});
	}

	/** Resolves a node's dynamic property options against its current configuration. */
	loadNodePropertyOptions(
		nodeType: string,
		input: LoadOptionsInputBody,
		signal?: AbortSignal
	): Promise<LoadOptionsResource> {
		return this.#transport.request('POST', `/node-types/${encodeURIComponent(nodeType)}/load-options`, {
			body: input,
			signal
		});
	}

	/** Resolves a resource mapper's columns. Shares load-options' request body deliberately. */
	loadNodePropertySchema(
		nodeType: string,
		input: LoadOptionsInputBody,
		signal?: AbortSignal
	): Promise<LoadSchemaResource> {
		return this.#transport.request('POST', `/node-types/${encodeURIComponent(nodeType)}/load-schema`, {
			body: input,
			signal
		});
	}

	/** Reads the expression surface the editor validates against, served rather than duplicated. */
	getExpressionGrammar(signal?: AbortSignal): Promise<ExpressionGrammar> {
		return this.#transport.request('GET', '/expression-grammar', { signal });
	}

	// --- Interop ---------------------------------------------------------------

	/**
	 * Imports an n8n workflow as a KilasFlow draft. The foreign document stays
	 * `unknown` — it is untrusted input — while the diagnostics and the minted
	 * webhook URLs are fully typed.
	 */
	importWorkflow(document: ImportWorkflowInputBody, signal?: AbortSignal): Promise<ImportedWorkflowResource> {
		return this.#transport.request('POST', '/workflows/import', { body: document, signal });
	}

	/** Exports a workflow as n8n-compatible JSON plus what the format could not carry. */
	exportWorkflow(workflowId: string, signal?: AbortSignal): Promise<ExportedWorkflowResource> {
		return this.#transport.request('GET', `/workflows/${encodeURIComponent(workflowId)}/export`, {
			query: { format: 'n8n' },
			signal
		});
	}

	// --- System ----------------------------------------------------------------

	/** Liveness: the process is up. Does not check dependencies. */
	getHealth(signal?: AbortSignal): Promise<HealthOutputBody> {
		return this.#transport.request('GET', '/health', { signal });
	}

	/** Readiness: the instance can serve requests, including database reachability. */
	getReady(signal?: AbortSignal): Promise<ReadyOutputBody> {
		return this.#transport.request('GET', '/ready', { signal });
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

/**
 * Shared configuration for every tenant client a host builds.
 *
 * One key per tenant: the server issues tenant-scoped keys and offers no
 * impersonation, so a host holds one `kfa1_…` key per customer and builds one
 * client per key. The per-tenant key itself is supplied later, to the
 * function {@link tenantClientFactory} returns.
 */
export interface TenantClientPoolOptions {
	/** Absolute base URL of the KilasFlow deployment, shared by every tenant. */
	baseUrl: string;
	/**
	 * Headers sent for every tenant — a gateway header, a `User-Agent`,
	 * tracing. Never an `Authorization` value: a shared credential here would
	 * be sent for the wrong tenant, which is the exact leak this factory
	 * exists to prevent.
	 */
	headers?: Record<string, string>;
	/** Injected for tests, or to add tracing/retries around the SDK. */
	fetch?: typeof globalThis.fetch;
	/** Aborts a request that takes too long. Defaults to 30 seconds. */
	timeoutMs?: number;
}

/**
 * Builds one fixed-credential client per tenant.
 *
 * The returned function takes a tenant's API key and returns a client that
 * sends only that key. The credential is fixed for the client's lifetime:
 * there is no setter, no refresh callback, and the constructor copies the
 * headers it is given, so holding one long-lived client and swapping its key
 * between requests — the bug that leaks one customer's data to another — is
 * not representable. Build a client per request, or cache one client per
 * tenant id; never mutate.
 *
 * Rotation is the boring answer on purpose: mint the new key, build a new
 * client with it, direct new work at the new client. In-flight requests on
 * the old client finish or fail on their own — a request authorized when it
 * started and revoked before it finished fails with a 401 the caller already
 * handles — and no callback invites a host to hold one client across the
 * rotation.
 */
export function tenantClientFactory(shared: TenantClientPoolOptions): (tenantApiKey: string) => KilasFlowClient {
	if (shared.headers?.Authorization !== undefined) {
		throw new Error('tenantClientFactory shared headers must not carry Authorization: the credential comes per tenant, not per pool');
	}
	return (tenantApiKey: string) => new KilasFlowClient({ ...shared, apiKey: tenantApiKey });
}
