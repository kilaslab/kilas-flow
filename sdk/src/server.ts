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
	ClearedDatastoreOutputBody,
	CreatedAPIKeyResource,
	CreateDatastoreInputBody,
	CreateTenantAPIKeyInputBody,
	CreateTenantInputBody,
	CreateTenantUserInputBody,
	CredentialBody,
	CredentialResource,
	CredentialTypeResource,
	CSVImportReport,
	DatastoreColumnInput,
	DatastoreColumnResource,
	DatastoreListOutputBody,
	DatastoreResource,
	Definition,
	DeleteRowsOutputBody,
	EmbedSessionResource,
	ExecutionListResource,
	ExecutionResource,
	ExecutionSummary,
	ExportedWorkflowResource,
	ExpressionGrammar,
	Filter,
	FilterCondition,
	GetDatastoreRow200,
	GetNodeIconTheme,
	HealthOutputBody,
	ImportedWorkflowResource,
	ImportWorkflowInputBody,
	IncrementRowsInputBody,
	IncrementRowsOutputBody,
	InsertDatastoreRow201,
	InsertRowInputBody,
	ListAPIKeysOutputBody,
	ListDatastoreRowsParams,
	ListTenantsOutputBody,
	ListTenantUsersOutputBody,
	LoadOptionsInputBody,
	LoadOptionsResource,
	LoadSchemaResource,
	LoginInputBody,
	PrincipalResource,
	ReadyOutputBody,
	RowListOutputBody,
	ScheduleBody,
	ScheduleResource,
	SetUserPasswordInputBody,
	StreamTicketResource,
	TenantDeletionResource,
	TenantResource,
	TestCredentialResource,
	TestPayloadBody,
	UpdateRowsInputBody,
	UpdateRowsOutputBody,
	UpsertRowInputBody,
	UpsertRowOutputBody,
	UserResource,
	WebhookRouteResource,
	WorkflowDiagnosticsResource,
	WorkflowDocumentInput,
	WorkflowPublishEventResource,
	WorkflowResource,
	WorkflowSummary,
	WorkflowVersionListResource,
	WorkflowVersionResource,
	ValidateWorkflowResource,
	EvalExpressionResource
} from './generated/models.js';

export type { TransportOptions } from './http.js';
export { KilasFlowError } from './http.js';

/**
 * Scopes an embed session may carry. Implication stays inside one family:
 * `workflow:write` and `workflow:run` imply `workflow:read`, and
 * `datastore:write` implies `datastore:read` — never across.
 */
export type EmbedScope = 'workflow:read' | 'workflow:write' | 'workflow:run' | 'datastore:read' | 'datastore:write';

export interface EmbedSessionRequest {
	/**
	 * Workflow this session may open. Exactly one of this and
	 * `datastoreId`: a session names one subject, never both, never neither.
	 */
	workflowId?: string;
	/**
	 * Datastore this session may touch. Exactly one of this and
	 * `workflowId`. A datastore session carries scopes from the
	 * `datastore:*` family only.
	 */
	datastoreId?: string;
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
 * A datastore column's declared type, matching n8n's four wire values. The
 * server normalises on read — numbers arrive as JSON numbers, booleans as
 * JSON booleans (SQLite stores 0/1 underneath), dates as strings — so a host
 * never branches on the driver.
 */
export type DatastoreColumnType = 'string' | 'number' | 'boolean' | 'date';

/** One column of a datastore create or add-column call. */
export interface DatastoreColumn {
	name: string;
	type: DatastoreColumnType;
}

/**
 * One datastore row as the API returns it: the system `id`, `createdAt` and
 * `updatedAt` beside one entry per user column. Values are JSON scalars —
 * dates travel as strings — keyed by column name.
 *
 * The shape is honestly dynamic: a datastore's columns are known at runtime,
 * not at compile time. Hosts that want static rows declare their own schema
 * extending this record and pass it as the `TRow` parameter on the row
 * methods; hosts that do not take the permissive default.
 */
export type DatastoreRow = Record<string, unknown>;

/**
 * Closed operator set for row filters, matching the server's ten supported
 * conditions. An unrecognised operator is a server-side 422 by design — the
 * operator slot is a predicate-bypass hole if it is ever a passthrough — so
 * the set is closed here too and a misspelling fails to compile.
 */
export type DatastoreFilterOperator =
	| 'eq'
	| 'neq'
	| 'like'
	| 'ilike'
	| 'gt'
	| 'gte'
	| 'lt'
	| 'lte'
	| 'isEmpty'
	| 'isNotEmpty';

/**
 * One filter predicate. `isEmpty` and `isNotEmpty` take no value — they test
 * for the absence of one — so the union withholds the slot rather than
 * leaving a caller to guess what to put in it.
 */
export type DatastoreFilterCondition =
	| { columnName: string; condition: 'isEmpty' | 'isNotEmpty' }
	| { columnName: string; condition: Exclude<DatastoreFilterOperator, 'isEmpty' | 'isNotEmpty'>; value: unknown };

/**
 * Builds the wire filter `{type, filters: [{columnName, condition, value}]}`
 * verbatim — the server is the authority on the shape, so the builder
 * produces it rather than inventing a friendlier dialect. The same object
 * the row-listing query triples express; the writes take it as JSON.
 */
export function datastoreFilter(type: 'and' | 'or', conditions: DatastoreFilterCondition[]): Filter {
	return {
		type,
		filters: conditions.map((predicate): FilterCondition => ({ ...predicate })) as FilterCondition[]
	};
}

/** One page of a cursor-paged listing: items plus the cursor for the next. */
export interface CursorPage<T> {
	items: T[] | null;
	nextCursor?: string;
}

/**
 * Pages a cursor listing to exhaustion, yielding items across every page.
 * One helper for every cursor surface — datastore rows and executions share
 * it — because a second copy is how two cursor formats drift apart. A page
 * without `nextCursor` ends the iteration.
 */
export async function* paginateCursor<T>(
	fetchPage: (cursor?: string) => Promise<CursorPage<T>>
): AsyncGenerator<T, void, void> {
	let cursor: string | undefined;
	for (;;) {
		const page = await fetchPage(cursor);
		for (const item of page.items ?? []) yield item;
		if (!page.nextCursor) return;
		cursor = page.nextCursor;
	}
}


/**
 * Refuses an empty row filter before it reaches the network. The server
 * answers an absent or empty filter with a 422 that removes nothing — an
 * empty filter never means "every row" — so the client rejects it first,
 * without sending, and without row values anywhere near the error. The
 * methods guarding this way are async so the refusal rejects rather than
 * throwing synchronously past a caller's `.catch()`.
 */
function requireRowFilter(filter: Filter, operation: string): void {
	if (!filter || !Array.isArray(filter.filters) || filter.filters.length === 0) {
		throw new Error(
			`${operation} needs a filter with at least one condition; an empty filter matches nothing by refusal, never everything`
		);
	}
}

/**
 * The longest key the `Idempotency-Key` header accepts: 255 characters, the
 * column width the server stores it in.
 */
export const MAX_IDEMPOTENCY_KEY_LENGTH = 255;

/**
 * Options for the writes that can be made idempotent: a manual run and the
 * two datastore row writes.
 *
 * The methods also take a bare `AbortSignal` where these options go, which is
 * what they took before keys existed. The two are told apart by shape rather
 * than by position, so an existing caller keeps working unchanged.
 */
export interface IdempotentWriteOptions {
	/**
	 * Value for the `Idempotency-Key` header, so a retry after a timeout is
	 * answered with the first request's outcome instead of a second side
	 * effect.
	 *
	 * One key per logical operation — a fresh UUID persisted *before* the
	 * first attempt — never a constant: keys are per tenant, so a constant
	 * makes two unrelated writes collide, and a retry under the same key is
	 * only a retry while the operation is the same one. A response that
	 * carried `Idempotent-Replayed: true` is that first outcome; the SDK
	 * returns the body and leaves the marker on the wire.
	 */
	idempotencyKey?: string;
	/** Aborts a request that takes too long. */
	signal?: AbortSignal;
}

/**
 * Options for a manual run: the idempotency key every retryable write takes,
 * and the revision to run.
 *
 * A run pinned to a revision is the same request with one more body field, so
 * it is the same method rather than a second one — and the field is named
 * `workflowVersionId` the way the API names it, not "revision", so a caller
 * reading both does not have to translate.
 */
export interface RunWorkflowOptions extends IdempotentWriteOptions {
	/**
	 * Runs this revision instead of the active one. A revision that is not
	 * this workflow's reads as missing (404) rather than being ignored.
	 */
	workflowVersionId?: string;
}

/** What the server accepts as a key: 1-255 printable ASCII, no spaces. */
const IDEMPOTENCY_KEY_PATTERN = /^[\x21-\x7e]{1,255}$/;

/**
 * Splits a write method's last argument into the signal and headers a request
 * carries, refusing a key the server would refuse.
 *
 * An unusable key throws rather than being dropped: sending no header would
 * look like idempotency to the caller while delivering none. The methods are
 * async so that throw becomes a rejection, matching `createEmbedSession`.
 */
function idempotentWrite(
	options: AbortSignal | IdempotentWriteOptions | undefined,
	operation: string
): { signal: AbortSignal | undefined; headers: Record<string, string> | undefined } {
	// Duck-typed, not `instanceof`: a host may hand the SDK a signal from
	// another realm (a worker, a test double) and the contract it needs is
	// the signal's shape, not its constructor.
	const isSignal = (value: unknown): value is AbortSignal =>
		typeof value === 'object' &&
		value !== null &&
		'aborted' in value &&
		typeof (value as AbortSignal).addEventListener === 'function';
	if (options === undefined || isSignal(options)) return { signal: options, headers: undefined };

	const key = options.idempotencyKey;
	if (key === undefined) return { signal: options.signal, headers: undefined };
	if (!IDEMPOTENCY_KEY_PATTERN.test(key)) {
		throw new Error(
			`${operation} idempotencyKey is sent as the Idempotency-Key header, which must be 1-${MAX_IDEMPOTENCY_KEY_LENGTH} printable ASCII characters without spaces`
		);
	}
	return { signal: options.signal, headers: { 'Idempotency-Key': key } };
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

	/**
	 * Reads the import report stored with a revision: what the n8n
	 * translation could not carry faithfully, per node and per field. A
	 * revision that was not imported answers with no source and no issues,
	 * so "clean import" and "never imported" stay distinguishable. Without a
	 * `versionId` the newest revision is read.
	 */
	getWorkflowDiagnostics(
		workflowId: string,
		options: { versionId?: string } = {},
		signal?: AbortSignal
	): Promise<WorkflowDiagnosticsResource> {
		return this.#transport.request('GET', `/workflows/${encodeURIComponent(workflowId)}/diagnostics`, {
			query: { versionId: options.versionId },
			signal
		});
	}

	/**
	 * Lists every webhook trigger's public address: the URL a sender is
	 * configured with, minted on the first read (or the first import) and
	 * reused forever, so it is known before activation and unchanged by it.
	 */
	listWorkflowWebhooks(workflowId: string, signal?: AbortSignal): Promise<WebhookRouteResource[]> {
		return this.#transport.request('GET', `/workflows/${encodeURIComponent(workflowId)}/webhooks`, { signal });
	}

	activateWorkflow(workflowId: string, signal?: AbortSignal): Promise<WorkflowResource> {
		return this.#transport.request('POST', `/workflows/${encodeURIComponent(workflowId)}/activate`, { signal });
	}

	deactivateWorkflow(workflowId: string, signal?: AbortSignal): Promise<WorkflowResource> {
		return this.#transport.request('POST', `/workflows/${encodeURIComponent(workflowId)}/deactivate`, { signal });
	}

	/**
	 * Queues a manual run and returns immediately with the execution record.
	 *
	 * With an `idempotencyKey`, a retry that carries the same key and the
	 * same input is answered with the first execution record instead of a
	 * second run — see {@link IdempotentWriteOptions} and the guide at
	 * `/guides/idempotency/`.
	 */
	// Async so a refused key rejects rather than throwing synchronously: a
	// caller awaits this, and a sync throw would escape their .catch().
	async runWorkflow(
		workflowId: string,
		input?: unknown,
		options?: AbortSignal | RunWorkflowOptions
	): Promise<{ id: string; status: string }> {
		const { signal, headers } = idempotentWrite(options, 'runWorkflow');
		const body: { input?: unknown; workflowVersionId?: string } = {};
		if (input !== undefined) body.input = input;
		// Duck-typed like the options above: a bare AbortSignal is what this
		// method took before either option existed, and it still works.
		if (options !== undefined && typeof options === 'object' && 'workflowVersionId' in options) {
			const revision = options.workflowVersionId;
			if (revision !== undefined && revision !== '') body.workflowVersionId = revision;
		}
		return this.#transport.request('POST', `/workflows/${encodeURIComponent(workflowId)}/run`, {
			body,
			signal,
			headers
		});
	}

	/**
	 * Compiles a document without saving it.
	 *
	 * The same compiler activation runs, so the diagnostics are the ones a
	 * save would have refused on — which is what makes this the check an
	 * agent runs before it writes anything. Nothing is stored, and an invalid
	 * document is a successful answer carrying `valid: false`.
	 */
	validateWorkflowDocument(document: WorkflowDocumentInput, signal?: AbortSignal): Promise<ValidateWorkflowResource> {
		return this.#transport.request('POST', '/workflows/validate', { body: document, signal });
	}

	/**
	 * Copies a workflow's newest revision into a new workflow under the same
	 * tenant, named `<name> (copy)` unless one is given.
	 */
	duplicateWorkflow(
		workflowId: string,
		options: { name?: string } = {},
		signal?: AbortSignal
	): Promise<WorkflowResource> {
		return this.#transport.request('POST', `/workflows/${encodeURIComponent(workflowId)}/duplicate`, {
			body: options.name === undefined ? undefined : { name: options.name },
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

	/**
	 * Queues a finished execution again, carrying its workflow, its revision
	 * and its input — the same queue path a manual run takes, with the
	 * revision named instead of resolved. An execution that is still queued,
	 * running or waiting is refused with 409.
	 */
	retryExecution(executionId: string, signal?: AbortSignal): Promise<ExecutionResource> {
		return this.#transport.request('POST', `/executions/${encodeURIComponent(executionId)}/retry`, { signal });
	}

	/**
	 * Evaluates one expression against a finished execution's stored node
	 * outputs, exactly as the runtime would have evaluated it during the run.
	 *
	 * Read-only: nothing is written and the workflow is not run again, so this
	 * is the cheap way to ask what a field held at a node. `nodeId` narrows
	 * the context to one node's output.
	 */
	evalExpression(
		executionId: string,
		expression: string,
		options: { nodeId?: string } = {},
		signal?: AbortSignal
	): Promise<EvalExpressionResource> {
		return this.#transport.request('POST', `/executions/${encodeURIComponent(executionId)}/eval`, {
			body: { expression, nodeId: options.nodeId },
			signal
		});
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

	// --- Tenants (operator surface) ----------------------------------------------
	//
	// The deployment's customers, their accounts, and keys minted on their
	// behalf. Every method here requires the operator credential — an API key
	// scoped to the operator tenant — and reaches across tenants, so a
	// customer's own key or any session is refused by the server. No embed
	// session may reach any of it: an embedded editor is bound to one workflow
	// or one datastore, never to the roster behind it.

	/** Lists every tenant in the deployment, oldest first, with its account count. */
	listTenants(signal?: AbortSignal): Promise<ListTenantsOutputBody> {
		return this.#transport.request('GET', '/tenants', { signal });
	}

	/**
	 * Adds a tenant. The ID is stable — it appears in URLs and on every row
	 * scoped to the tenant — so a taken ID is answered with a 409 rather than
	 * a silent reuse of the tenant already holding it.
	 */
	createTenant(input: CreateTenantInputBody, signal?: AbortSignal): Promise<TenantResource> {
		return this.#transport.request('POST', '/tenants', { body: input, signal });
	}

	/** Reads one tenant, such as the resource a create answered with. */
	getTenant(tenantId: string, signal?: AbortSignal): Promise<TenantResource> {
		return this.#transport.request('GET', `/tenants/${encodeURIComponent(tenantId)}`, { signal });
	}

	/**
	 * Deletes a tenant and everything it owns — executions and their payload
	 * files, workflows and versions, credentials, schedules, webhook
	 * deliveries, datastores and their physical tables, accounts, keys, and
	 * finally the tenant row — and answers with what was removed, per table.
	 * This is irreversible, and it needs the operator credential: a customer's
	 * key is refused like any other non-operator principal.
	 *
	 * The call is idempotent. Repeating it converges, so a deletion is
	 * confirmed by sending it again and reading zeros, and an interrupted one
	 * is resumed the same way. A tenant large enough can outlast this client's
	 * default `timeoutMs` (30s) or a proxy's idle timeout; the server keeps
	 * going when the request is abandoned, so a timed-out call is repeated
	 * rather than reported as a failure. A host that wants one call to wait can
	 * raise `timeoutMs` on the client.
	 */
	deleteTenant(tenantId: string, signal?: AbortSignal): Promise<TenantDeletionResource> {
		return this.#transport.request('DELETE', `/tenants/${encodeURIComponent(tenantId)}`, { signal });
	}

	/**
	 * Mints a key on another tenant's behalf and returns it in full exactly
	 * once. The tenant-scoped `/api-keys` can only mint for the caller, so a
	 * tenant created without this call would have no credential of its own.
	 */
	createTenantApiKey(
		tenantId: string,
		input: CreateTenantAPIKeyInputBody,
		signal?: AbortSignal
	): Promise<CreatedAPIKeyResource> {
		return this.#transport.request('POST', `/tenants/${encodeURIComponent(tenantId)}/api-keys`, {
			body: input,
			signal
		});
	}

	/** Lists one tenant's accounts. No password hash is ever included. */
	listTenantUsers(tenantId: string, signal?: AbortSignal): Promise<ListTenantUsersOutputBody> {
		return this.#transport.request('GET', `/tenants/${encodeURIComponent(tenantId)}/users`, { signal });
	}

	/** Creates an account that can sign in immediately with the password it was given. */
	createTenantUser(tenantId: string, input: CreateTenantUserInputBody, signal?: AbortSignal): Promise<UserResource> {
		return this.#transport.request('POST', `/tenants/${encodeURIComponent(tenantId)}/users`, {
			body: input,
			signal
		});
	}

	/**
	 * Stops an account signing in from the next request onwards. The row is
	 * kept, so the workflows and executions it authored still have a name,
	 * and {@link enableTenantUser} reverses the marker without touching the
	 * password.
	 */
	disableTenantUser(tenantId: string, userId: string, signal?: AbortSignal): Promise<UserResource> {
		return this.#transport.request(
			'POST',
			`/tenants/${encodeURIComponent(tenantId)}/users/${encodeURIComponent(userId)}/disable`,
			{ signal }
		);
	}

	/** Clears the offboarding marker, so the account signs in with the password it already had. */
	enableTenantUser(tenantId: string, userId: string, signal?: AbortSignal): Promise<UserResource> {
		return this.#transport.request(
			'POST',
			`/tenants/${encodeURIComponent(tenantId)}/users/${encodeURIComponent(userId)}/enable`,
			{ signal }
		);
	}

	/**
	 * Sets a new password: the operator's reset. The old password stops
	 * working at once, and sessions minted under it are cut loose by the
	 * account's password version rather than left to expire.
	 */
	setTenantUserPassword(
		tenantId: string,
		userId: string,
		input: SetUserPasswordInputBody,
		signal?: AbortSignal
	): Promise<UserResource> {
		return this.#transport.request(
			'POST',
			`/tenants/${encodeURIComponent(tenantId)}/users/${encodeURIComponent(userId)}/password`,
			{ body: input, signal }
		);
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

	// --- Datastores ----------------------------------------------------------

	/**
	 * The embed-scoped subset: the definition and row reads a datastore
	 * session may reach, plus the single-row insert proving the write scope.
	 * Every method is session-scoped by its datastore id — the same id the
	 * session was minted for — so a token bound to one table cannot name
	 * another. The management surface (columns, DDL, filtered writes, CSV
	 * transfer) follows below and stays with the backend key.
	 */

	/** Reads one datastore with its live columns in definition order. */
	getDatastore(datastoreId: string, signal?: AbortSignal): Promise<DatastoreResource> {
		return this.#transport.request('GET', `/datastores/${encodeURIComponent(datastoreId)}`, { signal });
	}

	/**
	 * Lists one datastore's rows in id order with the cursor for the next
	 * page. The flat filter triples are passed through as documented.
	 */
	listDatastoreRows(
		datastoreId: string,
		query: ListDatastoreRowsParams = {},
		signal?: AbortSignal
	): Promise<RowListOutputBody> {
		// The generated params mark the repeated triples nullable; the
		// transport has no encoding for null, so a null triple is sent as
		// absent rather than as a value.
		const { limit, cursor, match, columnName, condition, value } = query;
		return this.#transport.request('GET', `/datastores/${encodeURIComponent(datastoreId)}/rows`, {
			query: {
				limit,
				cursor,
				match,
				...(columnName ? { columnName } : {}),
				...(condition ? { condition } : {}),
				...(value ? { value } : {}),
			},
			signal,
		});
	}

	/** Returns one row by id. */
	getDatastoreRow(datastoreId: string, rowId: number, signal?: AbortSignal): Promise<GetDatastoreRow200> {
		return this.#transport.request(
			'GET',
			`/datastores/${encodeURIComponent(datastoreId)}/rows/${encodeURIComponent(String(rowId))}`,
			{ signal }
		);
	}

	// --- Datastore management ------------------------------------------------
	//
	// The backend surface: creating and destroying datastores, editing column
	// schemas, and filtered bulk row writes with a long-lived tenant API key.
	// None of it is reachable through an embed token by design — an embed
	// session is bound to one datastore and refused schema work outright —
	// so these methods send the host's own credential, never a session token.
	// A workflow SQL node can never read a datastore; the Datastore node and
	// this API are the only paths.

	/** Lists every datastore in the workspace. */
	listDatastores(signal?: AbortSignal): Promise<DatastoreListOutputBody> {
		return this.#transport.request('GET', '/datastores', { signal });
	}

	/**
	 * Creates a datastore; columns may arrive with it or be added afterwards
	 * through {@link addDatastoreColumn}. The column types are closed —
	 * string, number, boolean or date — matching what the server stores.
	 */
	createDatastore(input: { name: string; columns?: DatastoreColumn[] }, signal?: AbortSignal): Promise<DatastoreResource> {
		const body: CreateDatastoreInputBody = {
			name: input.name,
			...(input.columns ? { columns: input.columns.map((column): DatastoreColumnInput => ({ ...column })) } : {})
		};
		return this.#transport.request('POST', '/datastores', { body, signal });
	}

	/** Renames a datastore; columns change through the column methods. */
	renameDatastore(datastoreId: string, name: string, signal?: AbortSignal): Promise<DatastoreResource> {
		return this.#transport.request('PUT', `/datastores/${encodeURIComponent(datastoreId)}`, {
			body: { name },
			signal
		});
	}

	/** Deletes a datastore and every row it holds. Answers 204, so resolves void. */
	deleteDatastore(datastoreId: string, signal?: AbortSignal): Promise<void> {
		return this.#transport.request('DELETE', `/datastores/${encodeURIComponent(datastoreId)}`, { signal });
	}

	/** Deletes every row and keeps the schema. */
	clearDatastore(datastoreId: string, signal?: AbortSignal): Promise<ClearedDatastoreOutputBody> {
		return this.#transport.request('POST', `/datastores/${encodeURIComponent(datastoreId)}/clear`, { signal });
	}

	/** Appends one user column to a datastore. */
	addDatastoreColumn(datastoreId: string, column: DatastoreColumn, signal?: AbortSignal): Promise<DatastoreColumnResource> {
		return this.#transport.request('POST', `/datastores/${encodeURIComponent(datastoreId)}/columns`, {
			body: { ...column },
			signal
		});
	}

	/** Renames one user column. There is no retype: a column's type is fixed at creation. */
	renameDatastoreColumn(
		datastoreId: string,
		columnName: string,
		newName: string,
		signal?: AbortSignal
	): Promise<DatastoreColumnResource> {
		return this.#transport.request(
			'PUT',
			`/datastores/${encodeURIComponent(datastoreId)}/columns/${encodeURIComponent(columnName)}`,
			{ body: { name: newName }, signal }
		);
	}

	/** Removes one user column. Answers 204, so resolves void. */
	deleteDatastoreColumn(datastoreId: string, columnName: string, signal?: AbortSignal): Promise<void> {
		return this.#transport.request(
			'DELETE',
			`/datastores/${encodeURIComponent(datastoreId)}/columns/${encodeURIComponent(columnName)}`,
			{ signal }
		);
	}

	/**
	 * Sets columns on every row matching the filter and reads the touched
	 * rows back with the match count. The filter is required and must carry
	 * at least one condition: an empty filter is refused client-side rather
	 * than sent, because the server answers it with a 422 and never means
	 * "every row". The server exposes no dry-run parameter — the result
	 * envelope below is the whole answer — so there is nothing to expose.
	 */
	async updateDatastoreRows(
		datastoreId: string,
		filter: Filter,
		values: UpdateRowsInputBody['values'],
		signal?: AbortSignal
	): Promise<UpdateRowsOutputBody> {
		requireRowFilter(filter, 'updateDatastoreRows');
		return this.#transport.request('PUT', `/datastores/${encodeURIComponent(datastoreId)}/rows`, {
			body: { filter, values },
			signal
		});
	}

	/**
	 * Removes every row matching the filter and reads the removed rows back
	 * with the count. The signature takes no optional filter: a delete that
	 * cannot name its rows cannot be written, and an empty filter object is
	 * refused client-side rather than sent — a full-table wipe stays one
	 * forgotten argument away from impossible.
	 */
	async deleteDatastoreRows(datastoreId: string, filter: Filter, signal?: AbortSignal): Promise<DeleteRowsOutputBody> {
		requireRowFilter(filter, 'deleteDatastoreRows');
		return this.#transport.request('DELETE', `/datastores/${encodeURIComponent(datastoreId)}/rows`, {
			body: { filter },
			signal
		});
	}

	/**
	 * Updates every row matching the filter, or inserts one row from the
	 * values when nothing matches. Reports which of the two happened through
	 * `inserted`, with the affected rows either way.
	 *
	 * With an `idempotencyKey`, a retry is answered with this first outcome —
	 * `inserted` stays what the first attempt reported — instead of matching
	 * rows a second time. See {@link IdempotentWriteOptions}.
	 */
	async upsertDatastoreRow(
		datastoreId: string,
		filter: Filter,
		values: UpsertRowInputBody['values'],
		options?: AbortSignal | IdempotentWriteOptions
	): Promise<UpsertRowOutputBody> {
		requireRowFilter(filter, 'upsertDatastoreRow');
		const { signal, headers } = idempotentWrite(options, 'upsertDatastoreRow');
		return this.#transport.request('POST', `/datastores/${encodeURIComponent(datastoreId)}/rows/upsert`, {
			body: { filter, values },
			signal,
			headers
		});
	}

	/**
	 * Adds to a number column on every row matching the filter, in one
	 * statement, and resolves the rows as that statement left them. This is
	 * the counter write: {@link updateDatastoreRows} reads the row and then
	 * writes it, so two concurrent callers lose a write, while the server's
	 * increment is atomic per row and hands each caller its own value. The
	 * amount defaults to 1 and may be negative; an empty cell counts as
	 * zero. The filter is required and refused client-side when empty, like
	 * every other row write.
	 */
	async incrementDatastoreRows(
		datastoreId: string,
		filter: Filter,
		column: string,
		amount = 1,
		signal?: AbortSignal
	): Promise<IncrementRowsOutputBody> {
		requireRowFilter(filter, 'incrementDatastoreRows');
		const body: IncrementRowsInputBody = { filter, column, amount };
		return this.#transport.request('POST', `/datastores/${encodeURIComponent(datastoreId)}/rows/increment`, {
			body,
			signal
		});
	}

	/**
	 * Streams the datastore's rows as RFC 4180 CSV in id order — one header
	 * row plus one record per row — and resolves the file as text. The Accept
	 * header asks for text/csv; like the event-stream and icon URLs, a body
	 * that is not JSON does not travel through the JSON transport.
	 */
	exportDatastoreRows(
		datastoreId: string,
		options: { includeSystemColumns?: boolean } = {},
		signal?: AbortSignal
	): Promise<string> {
		return this.#transport.request('GET', `/datastores/${encodeURIComponent(datastoreId)}/rows/export`, {
			query: { includeSystemColumns: options.includeSystemColumns },
			accept: 'text/csv',
			signal
		});
	}

	/**
	 * Imports rows from a CSV file with a header row naming user columns.
	 * The file posts as raw text/csv rather than base64-in-JSON. The server
	 * validates every record before writing any row: a file with a failed
	 * row imports nothing, and the report names each failure with its line
	 * number. Row contents stay in the request body — never in error
	 * messages — so a refused import cannot leak row values into logs.
	 */
	importDatastoreRows(datastoreId: string, csv: string, signal?: AbortSignal): Promise<CSVImportReport> {
		return this.#transport.request('POST', `/datastores/${encodeURIComponent(datastoreId)}/rows/import`, {
			body: csv,
			contentType: 'text/csv',
			signal
		});
	}

	/**
	 * Pages a datastore's rows to exhaustion in id order, yielding one row
	 * at a time. `TRow` is the host's declared schema when it has one — see
	 * {@link DatastoreRow} — and the permissive record otherwise.
	 */
	async *iterateDatastoreRows<TRow extends DatastoreRow = DatastoreRow>(
		datastoreId: string,
		query: ListDatastoreRowsParams = {},
		signal?: AbortSignal
	): AsyncGenerator<TRow, void, void> {
		const { cursor: _, ...rest } = query;
		yield* paginateCursor<TRow>((cursor) =>
			this.listDatastoreRows(datastoreId, { ...rest, cursor }, signal).then((page) => ({
				items: (page.items ?? []) as TRow[],
				nextCursor: page.nextCursor
			}))
		);
	}

	/**
	 * Pages executions to exhaustion through the same {@link paginateCursor}
	 * helper as the row iterator, so the two cursor surfaces cannot drift.
	 */
	async *iterateExecutions(
		query: ExecutionListQuery = {},
		signal?: AbortSignal
	): AsyncGenerator<ExecutionSummary, void, void> {
		const { cursor: _, ...rest } = query;
		yield* paginateCursor<ExecutionSummary>((cursor) =>
			this.listExecutions({ ...rest, cursor }, signal).then((page) => ({
				items: page.items,
				nextCursor: page.nextCursor
			}))
		);
	}

	/**
	 * Writes one row and reads it back. Values are keyed by column name.
	 *
	 * With an `idempotencyKey`, a retry that carries the same key and the
	 * same values is answered with the first row — same id, same `Location` —
	 * instead of inserting a second one. See {@link IdempotentWriteOptions}.
	 */
	// Async so a refused key rejects rather than throwing synchronously.
	async insertDatastoreRow(
		datastoreId: string,
		values: InsertRowInputBody['values'],
		options?: AbortSignal | IdempotentWriteOptions
	): Promise<InsertDatastoreRow201> {
		const { signal, headers } = idempotentWrite(options, 'insertDatastoreRow');
		return this.#transport.request('POST', `/datastores/${encodeURIComponent(datastoreId)}/rows`, {
			body: { values },
			signal,
			headers
		});
	}

	// --- Embedding -----------------------------------------------------------

	/**
	 * Mints a short-lived session for one host origin, scoped to one
	 * workflow or one datastore.
	 *
	 * This is a backend call because it uses the host's own credentials. The
	 * resulting token is what the browser receives — never the API key.
	 */
	// Async so a validation failure rejects rather than throwing
	// synchronously: a caller awaits this, and a sync throw would escape their
	// .catch() and surface as an unhandled error instead.
	async createEmbedSession(request: EmbedSessionRequest, signal?: AbortSignal): Promise<EmbedSessionResource> {
		if ((!request.workflowId && !request.datastoreId) || (request.workflowId && request.datastoreId)) {
			throw new Error('createEmbedSession needs exactly one of workflowId and datastoreId');
		}
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
