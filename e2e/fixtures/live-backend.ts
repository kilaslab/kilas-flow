// Live backend fixtures (EPIC-87t47t). New file only: the harness in
// e2e/helpers/* and e2e/fixtures.ts is untouched, and the seed, library-import
// and waha-migration fixtures are reused by composition — imported, never
// rewritten.
//
// The suite proves KilasFlow works as an HTTP backend builder against the real
// binary: a workflow authored over REST becomes a callable HTTP API (webhook in,
// datastore/SQL out, Respond out), a real scheduler tick reaches an execution,
// and every delivery path — auth, response modes, dedupe, oversized bodies —
// is exercised through the same fetch the sender would use.
//
// Webhook URL rule: GET /workflows/{id}/webhooks reports every trigger's
// minted address, before or after activation, and POST /workflows/import
// reports the same addresses for an imported graph. Both read the same route
// table, so a natively authored workflow no longer needs the instance SQLite to
// learn its own URL (FEAT-cwmw90).
import { expect } from '@playwright/test';

import { readExecutionEvents, waitForExecution } from '../helpers/seed';
import { loadLibraryExport } from './library-import';
import { activateWorkflow, importN8nTemplate, saveDocument } from './waha-migration';

// Response shapes the live suites read. They are the fields the assertions
// touch, named here once so no spec re-declares them.
export interface WorkflowVersionResource {
	id: string;
	revision: number;
	document?: { name?: string; nodes?: unknown[]; connections?: unknown[] };
}

export interface WorkflowResource {
	id: string;
	name: string;
	active: boolean;
	latestVersion: WorkflowVersionResource;
}

export interface ExecutionSummaryRow {
	id: string;
	status: string;
	trigger: string;
	triggerNodeId?: string;
}

export interface ExecutionPage {
	items: ExecutionSummaryRow[];
	nextCursor?: string;
}

export interface ExecutionNodeRun {
	nodeId: string;
	status: string;
	output?: unknown;
	response?: unknown;
}

export interface ExecutionRecord {
	id: string;
	status: string;
	trigger: string;
	triggerNodeId?: string;
	// input is the shaped trigger item the run started from — the n8n webhook
	// envelope, including the verified `jwtPayload` when the delivery was a
	// signed JWT. It is the contract an imported workflow reads `$json` from.
	input?: unknown;
	nodeRuns?: ExecutionNodeRun[];
	startedAt?: string;
	finishedAt?: string;
	// Present only while the execution waits, the same way the API documents
	// it: a machine resume link and, for approval waits, a human page.
	resumeUrl?: string;
	approvalUrl?: string;
}

// liveApi is the one fetcher every live suite speaks, in the same shape the
// other fixtures' api() helpers use: the real REST surface, exact status
// asserted, body returned as JSON. The error names the method, path, both
// statuses and the response body, so a failure reads as the server's answer
// rather than as `undefined is not a function` three lines later. The caller
// names the response shape it reads; the default is unknown.
export async function liveApi<T = unknown>(
	baseURL: string,
	method: string,
	path: string,
	body?: unknown,
	wantStatus = 200
): Promise<T> {
	const response = await fetch(`${baseURL}/api/v1${path}`, {
		method,
		headers: { 'content-type': 'application/json' },
		body: body === undefined ? undefined : JSON.stringify(body)
	});
	if (response.status !== wantStatus) {
		throw new Error(`${method} ${path}: status ${response.status}, want ${wantStatus} (body: ${await response.text()})`);
	}
	if (response.status === 204) return null as T;
	return (await response.json()) as T;
}

export interface MintedWebhookWorkflow {
	workflowId: string;
	url: string;
	nodeId: string;
}

// mintWebhookWorkflows imports the pinned webhook-echo sample once per
// requested workflow, keeps each import's minted route, re-saves the native
// document (node ids preserved, so the route survives the re-save), activates,
// and delivers one probe.
//
// The probe is the route-stability proof this fixture exists for: the public
// URL is minted per trigger node and must survive every re-save and
// re-activation, because a sender configured against it has no way to learn a
// new one. A probe that does not answer below 300 means the route changed or
// the activation did not bind — both are BUGs under EPIC-87t47t, never a test
// to relax.
export async function mintWebhookWorkflows(
	baseURL: string,
	count: number,
	labelPrefix: string
): Promise<MintedWebhookWorkflow[]> {
	const template = await loadLibraryExport('webhook-echo');
	const minted: MintedWebhookWorkflow[] = [];
	for (let index = 0; index < count; index += 1) {
		const imported = await importN8nTemplate(baseURL, template, `${labelPrefix} ${index + 1}`);
		const webhook = imported.webhooks?.[0];
		if (!webhook || !/^\/webhook\/[0-9a-f]{32}$/.test(webhook.url)) {
			throw new Error(
				`POST /workflows/import did not report a minted webhook route (got ${JSON.stringify(imported.webhooks)})`
			);
		}
		// The native document, byte for byte, with the webhook node's id intact:
		// the route table is keyed by (workflow, node id), so preserving the id
		// is what keeps `webhook.url` valid after this save and the activation
		// below.
		await saveDocument(baseURL, imported.workflow.id, imported.workflow.latestVersion.document);
		await activateWorkflow(baseURL, imported.workflow.id);

		const probe = await deliver(baseURL, webhook.url, { json: { text: 'mint probe' } });
		if (probe.status >= 300) {
			throw new Error(
				`the minted route ${webhook.url} stopped answering after re-save + re-activate ` +
					`(status ${probe.status}, body ${probe.text}); see EPIC-87t47t and the server log for the binding change`
			);
		}
		minted.push({ workflowId: imported.workflow.id, url: webhook.url, nodeId: webhook.nodeId });
	}
	return minted;
}

export interface DeliveryOptions {
	method?: string;
	headers?: Record<string, string>;
	// rawBody sends the bytes verbatim, which is what a signature check or an
	// oversized-body refusal has to see. json stringifies and sets the content
	// type. Sending neither sends no body at all.
	rawBody?: string;
	json?: unknown;
}

export interface DeliveryResult {
	status: number;
	text: string;
	json?: unknown;
	headers: Record<string, string>;
}

// deliver is the one delivery helper every live suite uses: the same fetch a
// real sender would make against the minted webhook URL, returning the status,
// the raw text, the parsed body when it is JSON, and the headers.
export async function deliver(
	baseURL: string,
	url: string,
	opts: DeliveryOptions = {}
): Promise<DeliveryResult> {
	const headers: Record<string, string> = { ...(opts.headers ?? {}) };
	let body: string | undefined;
	if (opts.rawBody !== undefined) {
		body = opts.rawBody;
	} else if (opts.json !== undefined) {
		body = JSON.stringify(opts.json);
		if (!Object.keys(headers).some((name) => name.toLowerCase() === 'content-type')) {
			headers['content-type'] = 'application/json';
		}
	}
	const response = await fetch(`${baseURL}${url}`, { method: opts.method ?? 'POST', headers, body });
	const text = await response.text();
	let parsed: unknown;
	try {
		parsed = JSON.parse(text);
	} catch {
		parsed = undefined;
	}
	const responseHeaders: Record<string, string> = {};
	response.headers.forEach((value, name) => {
		responseHeaders[name] = value;
	});
	return { status: response.status, text, json: parsed, headers: responseHeaders };
}

// waitForTriggerExecution awaits a new execution of one workflow under one
// trigger, drives it to terminal, and reads its event feed so the terminal
// event is observed rather than assumed. Polling is the only waiting: an
// execution that is merely late must not turn into a fixed sleep that hides a
// scheduler that never fired.
export async function waitForTriggerExecution(
	baseURL: string,
	workflowId: string,
	trigger: string,
	beforeIds: string[],
	timeoutMs = 90_000
): Promise<ExecutionRecord> {
	const before = new Set(beforeIds);
	let found = '';
	await expect
		.poll(
			async () => {
				const page = await liveApi<ExecutionPage>(
					baseURL,
					'GET',
					`/executions?workflowId=${encodeURIComponent(workflowId)}&trigger=${encodeURIComponent(trigger)}&limit=25`
				);
				const item = page.items.find((row) => !before.has(row.id));
				found = item?.id ?? '';
				return found || null;
			},
			{ message: `a new ${trigger} execution of ${workflowId} appears`, timeout: timeoutMs }
		)
		.not.toBeNull();

	// waitForExecution predates the typed live fixtures and returns any; the
	// record is read back from GET /executions/{id}, whose shape is fixed by
	// ExecutionResource above.
	const record = (await waitForExecution(baseURL, found)) as ExecutionRecord;
	const events = await readExecutionEvents(baseURL, found);
	const terminal = events.some(
		(event) =>
			event.type === 'execution.completed' ||
			event.type === 'execution.failed' ||
			event.type === 'execution.cancelled'
	);
	expect(terminal, `execution ${found} ended its event feed without a terminal event`).toBe(true);
	return record;
}

// pgCredentialFields turns an integration DSN into the credential fields a
// postgres node actually takes (nodes/database_test.go liveCredential, field
// for field). A node never sees a DSN; it is assembled from credential fields,
// so this is the translation the gated external-database test performs.
export function pgCredentialFields(dsn: string): Record<string, string> {
	const parsed = new URL(dsn);
	return {
		host: parsed.hostname,
		port: parsed.port || '5432',
		database: parsed.pathname.replace(/^\//, ''),
		user: decodeURIComponent(parsed.username),
		password: decodeURIComponent(parsed.password),
		// The driver reads an absent sslmode as disable, which is what a
		// throwaway test server speaks; carrying the query's own value keeps a
		// TLS-required server working.
		sslMode: parsed.searchParams.get('sslmode') ?? 'disable'
	};
}
