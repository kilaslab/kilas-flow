import { readFile } from 'node:fs/promises';
import { expect } from '@playwright/test';

import { createCredential, readExecutionEvents, waitForExecution } from '../helpers/seed';

// WAHA-migration fixtures (V2-p11-4). Everything the waha-migration spec
// needs beyond the shared harness: the digest-pinned chatting template, the
// n8n import call, WAHA credential binding, and webhook delivery.
//
// Licensing: the WAHA templates repository carries no licence file, so the
// template is fetched by scripts/corpus-sync.sh into the gitignored .corpus/
// directory and is never committed. Every helper that needs it returns null
// when the corpus is absent and the spec skips with a message instead of
// failing — a machine that has not run `make corpus` is not broken.

export interface ImportIssue {
	severity: 'blocking' | 'lossy' | 'dropped';
	nodeName?: string;
	nodeId?: string;
	field?: string;
	type?: string;
	reason: string;
}

export interface WebhookRoute {
	nodeId: string;
	method: string;
	path: string;
	url: string;
}

export interface ImportedWorkflow {
	workflow: { id: string; name: string; latestVersion: { document: WorkflowDocument } };
	unsupported: ImportIssue[];
	webhooks: WebhookRoute[];
}

export interface WorkflowNode {
	id: string;
	name: string;
	type: string;
	typeVersion: number;
	position?: unknown;
	parameters?: Record<string, unknown>;
	credentials?: Record<string, string>;
}

export interface WorkflowDocument {
	schemaVersion: number;
	name: string;
	nodes: WorkflowNode[];
	connections: unknown[];
	settings?: Record<string, unknown>;
}

async function api(baseURL: string, method: string, path: string, body?: unknown, wantStatus = 200): Promise<any> {
	const response = await fetch(`${baseURL}/api/v1${path}`, {
		method,
		headers: { 'content-type': 'application/json' },
		body: body === undefined ? undefined : JSON.stringify(body)
	});
	if (response.status !== wantStatus) {
		throw new Error(`${method} ${path}: status ${response.status}, want ${wantStatus} (body: ${await response.text()})`);
	}
	if (response.status === 204) return null;
	return response.json();
}

// The official WAHA chatting template: reply "pong" to "ping", send an image
// on "image", always send seen. Read from the gitignored corpus, never
// committed (see scripts/corpus-sync.sh for the licensing rationale).
export async function loadChattingTemplate(): Promise<Record<string, unknown> | null> {
	const url = new URL('../../.corpus/waha-templates/chatting-template/template.json', import.meta.url);
	try {
		return JSON.parse(await readFile(url, 'utf-8')) as Record<string, unknown>;
	} catch (error) {
		if ((error as NodeJS.ErrnoException)?.code === 'ENOENT') return null;
		throw error;
	}
}

export async function importN8nTemplate(
	baseURL: string,
	template: Record<string, unknown>,
	name?: string
): Promise<ImportedWorkflow> {
	return api(baseURL, 'POST', '/workflows/import', {
		format: 'n8n',
		workflow: template,
		...(name ? { name } : {})
	}, 201) as Promise<ImportedWorkflow>;
}

export async function fetchDocument(baseURL: string, workflowId: string): Promise<WorkflowDocument> {
	const stored = await api(baseURL, 'GET', `/workflows/${workflowId}`);
	return stored.latestVersion.document as WorkflowDocument;
}

export async function saveDocument(baseURL: string, workflowId: string, document: WorkflowDocument): Promise<void> {
	await api(baseURL, 'PUT', `/workflows/${workflowId}`, {
		schemaVersion: document.schemaVersion,
		name: document.name,
		nodes: document.nodes,
		connections: document.connections,
		settings: document.settings ?? {}
	});
}

// A wahaApi credential pointed at the loopback stub: the pack builds every
// request URL from the non-secret baseUrl field, so the stub stands in for a
// real WAHA server and the reply is observed as an HTTP request. A real WAHA
// server is reserved for the V2-p11-8 capstone; this proves the path.
export async function createStubWahaCredential(baseURL: string, name: string, stubBaseUrl: string): Promise<string> {
	const created = await createCredential(baseURL, {
		name,
		type: 'wahaApi',
		fields: { baseUrl: stubBaseUrl, apiKey: 'e2e-stub-key' }
	});
	return created.id;
}

// WAHA action and trigger nodes arrive from n8n unbound — an n8n credential
// reference is instance-local — so the migrating user rebinds them. Returns
// the bound node names for assertions.
export function bindWahaCredential(document: WorkflowDocument, credentialId: string): string[] {
	const bound: string[] = [];
	for (const node of document.nodes) {
		if (node.type === 'pack.waha' || node.type === 'pack.wahaTrigger') {
			node.credentials = { ...(node.credentials ?? {}), wahaApi: credentialId };
			bound.push(node.name);
		}
	}
	return bound;
}

// The template's Send Image node carries no file — n8n templates leave the
// media for the user — while the pack requires one. Completing the migration
// means supplying it, the same edit a user makes in the parameter panel, and
// activation stays refused until it is there.
export function completeChattingMigration(document: WorkflowDocument, imageUrl: string): string[] {
	const completed: string[] = [];
	for (const node of document.nodes) {
		if (node.type === 'pack.waha' && node.parameters?.['operation'] === 'Send Image') {
			node.parameters = { ...(node.parameters ?? {}), file: imageUrl };
			completed.push(node.name);
		}
	}
	return completed;
}

export async function activateWorkflow(baseURL: string, workflowId: string): Promise<any> {
	return api(baseURL, 'POST', `/workflows/${workflowId}/activate`, {});
}

// A recorded WAHA "message" delivery: the trigger's contract is an HTTP POST
// with this body shape (event + session + payload), optionally carrying an
// X-Webhook-Hmac header, and no WAHA server is needed to prove the path.
export function wahaPingDelivery(session = 'default'): Record<string, unknown> {
	return {
		event: 'message',
		session,
		payload: {
			from: 'e2e-user@s.whatsapp.net',
			body: 'ping',
			_pushName: 'e2e',
			id: 'e2e-delivery-1'
		}
	};
}

export async function deliverWebhook(baseURL: string, url: string, body: unknown): Promise<number> {
	const response = await fetch(`${baseURL}${url}`, {
		method: 'POST',
		headers: { 'content-type': 'application/json' },
		body: JSON.stringify(body)
	});
	return response.status;
}

export interface ExecutionSummary {
	id: string;
	workflowId: string;
	status: string;
}

export async function listExecutions(baseURL: string, workflowId: string): Promise<ExecutionSummary[]> {
	const page = await api(baseURL, 'GET', `/executions?workflowId=${encodeURIComponent(workflowId)}&limit=100`);
	return page.items as ExecutionSummary[];
}

// The newest execution for a workflow once one exists past `before` ids,
// awaited with assertions rather than sleeps, then driven to terminal.
export async function waitForNewExecution(
	baseURL: string,
	workflowId: string,
	before: string[]
): Promise<any> {
	const known = new Set(before);
	let found: ExecutionSummary | undefined;
	await expect
		.poll(async () => {
			const items = await listExecutions(baseURL, workflowId);
			found = items.find((item) => !known.has(item.id));
			return found?.id ?? '';
		}, { message: `a new execution for workflow ${workflowId}`, timeout: 60_000 })
		.not.toBe('');
	const record = await waitForExecution(baseURL, found!.id);
	const events = await readExecutionEvents(baseURL, found!.id);
	return { record, events };
}
