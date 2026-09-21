import { expect } from '@playwright/test';

// API seeding for end-to-end tests. Everything a test needs — workflows,
// credentials, schedules, runs, embed sessions — is created through the
// public REST API, the same surface a host application uses. Nothing writes
// GORM rows directly, so the seeders keep working when migrations change the
// schema.
//
// Authentication is off in the test instances (the product default), so every
// call is already scoped to the bootstrapped default tenant: there is no
// tenant endpoint to seed through and no login step to perform.

export interface SeedWorkflow {
	id: string;
	name: string;
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


export function manualWorkflowDocument(name: string): Record<string, unknown> {
	return {
		schemaVersion: 1,
		name,
		nodes: [{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } }],
		connections: [],
		settings: {}
	};
}

export function chatWorkflowDocument(name: string): Record<string, unknown> {
	return {
		schemaVersion: 1,
		name,
		nodes: [
			{
				id: 'chat',
				name: 'When chat message received',
				type: 'kilasflow.chatTrigger',
				typeVersion: 1,
				position: { x: 0, y: 0 }
			},
			{
				id: 'set',
				name: 'Reply',
				type: 'kilasflow.set',
				typeVersion: 1,
				position: { x: 240, y: 0 },
				parameters: {
					assignments: { output: { mode: 'expression', value: '{{ $json.chatInput }}' } }
				}
			}
		],
		connections: [
			{
				id: 'c1',
				kind: 'main',
				source: { nodeId: 'chat', port: 'main' },
				target: { nodeId: 'set', port: 'main' }
			}
		],
		settings: {}
	};
}

// A manual trigger driving one HTTP node. The workflow under test calls the
// local stub through the outbound policy; the suite never touches the internet.
export function httpWorkflowDocument(name: string, url: string): Record<string, unknown> {
	return {
		schemaVersion: 1,
		name,
		nodes: [
			{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
			{
				id: 'http',
				name: 'Call stub',
				type: 'kilasflow.httpRequest',
				typeVersion: 1,
				position: { x: 240, y: 0 },
				parameters: { method: 'GET', url }
			}
		],
		connections: [
			{
				id: 'c1',
				kind: 'main',
				source: { nodeId: 'manual', port: 'main' },
				target: { nodeId: 'http', port: 'main' }
			}
		],
		settings: {}
	};
}

export async function createWorkflow(baseURL: string, document: Record<string, unknown>): Promise<SeedWorkflow> {
	const created = await api(baseURL, 'POST', '/workflows', document, 201);
	return { id: created.id, name: created.name ?? document.name };
}

export async function createCredential(
	baseURL: string,
	credential: { name: string; type: string; fields: Record<string, string> }
): Promise<{ id: string }> {
	const created = await api(baseURL, 'POST', '/credentials', credential, 201);
	return { id: created.id };
}
export async function createSchedule(
	baseURL: string,
	schedule: { workflowId: string; cron: string; active: boolean }
): Promise<{ id: string }> {
	const created = await api(baseURL, 'POST', '/schedules', schedule, 201);
	return { id: created.id };
}

export async function listSchedules(baseURL: string): Promise<any[]> {
	return api(baseURL, 'GET', '/schedules');
}

export async function runWorkflow(baseURL: string, workflowId: string, input?: unknown): Promise<string> {
	const body = input === undefined ? {} : { input };
	const run = await api(baseURL, 'POST', `/workflows/${workflowId}/run`, body, 202);
	return run.id;
}
const TERMINAL_STATUSES = ['succeeded', 'failed', 'cancelled'];

export async function waitForExecution(baseURL: string, executionId: string): Promise<any> {
	await expect
		.poll(
			async () => TERMINAL_STATUSES.includes((await api(baseURL, 'GET', `/executions/${executionId}`)).status),
			{ message: `execution ${executionId} reaches a terminal status`, timeout: 60_000 }
		)
		.toBe(true);
	return api(baseURL, 'GET', `/executions/${executionId}`);
}

export interface ExecutionEvent {
	type: string;
	data: string;
}

// Reads the live event feed for one execution until a terminal event arrives.
// A finished execution replays its retained events first, so this also works
// after waitForExecution: the terminal event is still observed, not assumed.
export async function readExecutionEvents(baseURL: string, executionId: string): Promise<ExecutionEvent[]> {
	const response = await fetch(`${baseURL}/api/v1/executions/${executionId}/events`, {
		headers: { accept: 'text/event-stream' }
	});
	if (!response.ok || !response.body) {
		throw new Error(`GET /executions/${executionId}/events: status ${response.status}`);
	}
	const events: ExecutionEvent[] = [];
	const reader = response.body.getReader();
	const decoder = new TextDecoder();
	let buffer = '';
	const deadline = Date.now() + 30_000;
	try {
		for (;;) {
			if (Date.now() > deadline) throw new Error(`SSE for execution ${executionId} produced no terminal event in time`);
			const { done, value } = await reader.read();
			if (value) buffer += decoder.decode(value, { stream: true });
			let boundary = buffer.indexOf('\n\n');
			while (boundary !== -1) {
				const block = buffer.slice(0, boundary);
				buffer = buffer.slice(boundary + 2);
				let type = '';
				const data: string[] = [];
				for (const line of block.split('\n')) {
					if (line.startsWith('event:')) type = line.slice(6).trim();
					else if (line.startsWith('data:')) data.push(line.slice(5).trim());
				}
				if (type) {
					events.push({ type, data: data.join('\n') });
					if (type === 'execution.completed' || type === 'execution.failed' || type === 'execution.cancelled') {
						return events;
					}
				}
				boundary = buffer.indexOf('\n\n');
			}
			if (done) throw new Error(`SSE for execution ${executionId} ended without a terminal event`);
		}
	} finally {
		reader.releaseLock();
		await response.body.cancel().catch(() => undefined);
	}
}

export async function createEmbedSession(
	baseURL: string,
	session: { workflowId: string; origin: string }
): Promise<{ token: string; embedUrl: string; scopes: string[]; origin: string }> {
	return api(
		baseURL,
		'POST',
		'/embed-sessions',
		{ workflowId: session.workflowId, scopes: ['workflow:read', 'workflow:write', 'workflow:run'], origin: session.origin },
		201
	);
}
