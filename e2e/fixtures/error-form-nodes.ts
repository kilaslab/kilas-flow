import { expect } from '@playwright/test';

import { readExecutionEvents, waitForExecution } from '../helpers/seed';
import {
	deliver,
	liveApi,
	waitForTriggerExecution,
	type ExecutionPage,
	type ExecutionRecord,
	type WorkflowResource
} from './live-backend';

// The three catalogue entries neither tier covered: the error pair
// (kilasflow.errorTrigger, kilasflow.stopAndError) and the hosted form
// (kilasflow.formTrigger). Both ratchets read GET /node-types live, so adding
// an entry to a tier list without a test that executes it would be a claim, not
// coverage — this fixture is what the two tier entries stand for, and both
// spec files call it.
//
// New file, so the two spec files (already edited by other tickets) only gain
// an import, a tier block and one test each.

interface DocNode {
	id: string;
	name: string;
	type: string;
	typeVersion: number;
	position: { x: number; y: number };
	parameters?: Record<string, unknown>;
}

function docNode(id: string, name: string, type: string, parameters?: Record<string, unknown>): DocNode {
	const entry: DocNode = { id, name, type, typeVersion: 1, position: { x: 0, y: 0 } };
	if (parameters !== undefined) entry.parameters = parameters;
	return entry;
}

interface DocConnection {
	id: string;
	kind: string;
	source: { nodeId: string; port: string };
	target: { nodeId: string; port: string };
}

function main(id: string, source: string, target: string): DocConnection {
	return { id, kind: 'main', source: { nodeId: source, port: 'main' }, target: { nodeId: target, port: 'main' } };
}

// The execution resource carries `error` as the structured `{code, message}`
// the engine writes on a failed run; the shared fixture predates a caller that
// reads it, so it is named here rather than widening live-backend.ts.
interface FailedExecution extends ExecutionRecord {
	error?: { code?: string; message?: string };
}

async function createWorkflow(
	baseURL: string,
	name: string,
	nodes: DocNode[],
	connections: DocConnection[],
	settings: Record<string, unknown> = {}
): Promise<WorkflowResource> {
	return liveApi<WorkflowResource>(baseURL, 'POST', '/workflows', { schemaVersion: 1, name, nodes, connections, settings }, 201);
}

async function runToTerminal(baseURL: string, workflowId: string): Promise<FailedExecution> {
	const started = await liveApi<{ id: string }>(baseURL, 'POST', `/workflows/${workflowId}/run`, {}, 202);
	return (await waitForExecution(baseURL, started.id)) as FailedExecution;
}

// The recorded output is one stream per output port, each a list of items
// whose `json` is the payload. The shared fixture types it as unknown, so the
// caller names the item shape it reads — the same contract liveApi has.
interface RecordedNodeRun {
	nodeId: string;
	status: string;
	output?: Array<Array<{ json: unknown }>>;
}

function outputItem<T = Record<string, unknown>>(record: ExecutionRecord, nodeId: string): T {
	const run = (record.nodeRuns as RecordedNodeRun[] | undefined)?.find((entry) => entry.nodeId === nodeId);
	expect(run, `node ${nodeId} has a recorded run`).toBeDefined();
	expect(run?.status).toBe('succeeded');
	return run?.output?.[0]?.[0]?.json as T;
}

// The item the Error Trigger emits, which is n8n's own payload.
interface ErrorTriggerItem {
	execution: { id: string; mode: string; error: { message: string; node?: { id: string; type: string } } };
	workflow: { id: string };
	trigger: { mode: string };
}

// Stop and Error fails a run with the author's message; the failing workflow
// names an error workflow in its settings, and that workflow — activated,
// because the engine only starts one it can run as active — receives the
// failure on its Error Trigger. The pair is exercised together because neither
// does anything alone: the trigger only ever fires for a run that failed.
export async function exerciseErrorWorkflow(baseURL: string, label: string): Promise<void> {
	const message = `${label}: declared failure`;
	const handler = await createWorkflow(
		baseURL,
		`${label} Error Handler`,
		[
			docNode('trigger', 'Error Trigger', 'kilasflow.errorTrigger'),
			docNode('note', 'Note', 'kilasflow.set', { assignments: { handled: 'yes' } })
		],
		[main('c1', 'trigger', 'note')]
	);
	// An inactive handler is not an error workflow: the engine refuses to start
	// it and logs the reason, and nothing runs.
	await liveApi(baseURL, 'POST', `/workflows/${handler.id}/activate`, {});

	const failing = await createWorkflow(
		baseURL,
		`${label} Failing`,
		[docNode('manual', 'Manual Trigger', 'kilasflow.manual'), docNode('stop', 'Stop', 'kilasflow.stopAndError', { errorMessage: message })],
		[main('c1', 'manual', 'stop')],
		{ errorWorkflow: handler.id }
	);
	const failed = await runToTerminal(baseURL, failing.id);
	expect(failed.status).toBe('failed');
	// The runner names the node that failed and the cause it carried, which is
	// the sentence an operator reads in the execution list.
	expect(failed.error?.message).toBe(`execute node "stop": ${message}`);

	// The error run is a consequence of the failed one and starts after it is
	// terminal, so it is awaited by its own execution row rather than by a
	// sleep.
	let handlerExecutionId = '';
	await expect
		.poll(
			async () => {
				const page = await liveApi<ExecutionPage>(baseURL, 'GET', `/executions?workflowId=${encodeURIComponent(handler.id)}&limit=10`);
				handlerExecutionId = page.items[0]?.id ?? '';
				return handlerExecutionId;
			},
			{ message: `the error workflow ${handler.id} starts for the failed run`, timeout: 60_000 }
		)
		.not.toBe('');

	const handled = (await waitForExecution(baseURL, handlerExecutionId)) as ExecutionRecord;
	expect(handled.status).toBe('succeeded');
	const events = await readExecutionEvents(baseURL, handlerExecutionId);
	expect(events.map((event) => event.type)).toContain('execution.completed');

	// The Error Trigger emits n8n's own error payload, because the workflow on
	// the other side is usually one imported from n8n and reads
	// `{{ $json.execution.error.message }}`.
	const received = outputItem<ErrorTriggerItem>(handled, 'trigger');
	expect(received.execution.id).toBe(failed.id);
	expect(received.execution.error.message).toContain(message);
	expect(received.execution.error.node).toMatchObject({ id: 'stop', type: 'kilasflow.stopAndError' });
	expect(received.workflow).toMatchObject({ id: failing.id });
	expect(received.trigger).toMatchObject({ mode: 'manual' });
	expect(outputItem(handled, 'note')).toMatchObject({ handled: 'yes' });
}

// The hosted form on both sides of its minted route: a manual run emits the
// trigger item like every other trigger, the activated route serves the page
// on GET, refuses a submission that omits a required field without running
// anything, and starts the run on a complete POST.
export async function exerciseFormTrigger(baseURL: string, label: string): Promise<void> {
	const title = `${label} sign up`;
	const form = await createWorkflow(
		baseURL,
		`${label} Form`,
		[
			docNode('form', 'Form', 'kilasflow.formTrigger', {
				path: 'signup',
				formTitle: title,
				formFields: { values: [{ fieldLabel: 'Name', fieldType: 'text', requiredField: true }] }
			}),
			docNode('out', 'Out', 'kilasflow.set', { assignments: { v: 'form-ok' } })
		],
		[main('c1', 'form', 'out')]
	);

	const manual = await runToTerminal(baseURL, form.id);
	expect(manual.status).toBe('succeeded');
	expect(outputItem(manual, 'out')).toMatchObject({ v: 'form-ok' });

	// GET and POST share one minted route: the submission method is the only
	// one bonded, and the page is served from the route itself.
	const routes = await liveApi<Array<{ nodeId: string; method: string; url: string }>>(baseURL, 'GET', `/workflows/${form.id}/webhooks`);
	expect(routes).toHaveLength(1);
	expect(routes[0].method).toBe('POST');
	expect(routes[0].url).toMatch(/^\/webhook\/[0-9a-f]{32}$/);
	await liveApi(baseURL, 'POST', `/workflows/${form.id}/activate`, {});

	const page = await deliver(baseURL, routes[0].url, { method: 'GET' });
	expect(page.status).toBe(200);
	expect(page.headers['content-type']).toContain('text/html');
	expect(page.text).toContain(title);
	expect(page.text).toContain('name="Name"');

	const before = (await liveApi<ExecutionPage>(baseURL, 'GET', `/executions?workflowId=${form.id}&limit=100`)).items.map((item) => item.id);
	const urlencoded = { 'content-type': 'application/x-www-form-urlencoded' };
	const refused = await deliver(baseURL, routes[0].url, { headers: urlencoded, rawBody: 'Other=1' });
	expect(refused.status).toBe(400);
	expect(refused.text).toContain('Name');
	const accepted = await deliver(baseURL, routes[0].url, { headers: urlencoded, rawBody: 'Name=Ada' });
	expect(accepted.status).toBe(200);
	expect(accepted.headers['content-type']).toContain('text/html');

	const submitted = await waitForTriggerExecution(baseURL, form.id, 'webhook', before);
	expect(submitted.status).toBe('succeeded');
	// The refused submission started nothing: exactly one execution is new.
	const after = (await liveApi<ExecutionPage>(baseURL, 'GET', `/executions?workflowId=${form.id}&limit=100`)).items.map((item) => item.id);
	expect(after.filter((id) => !before.includes(id))).toEqual([submitted.id]);

	// The submission arrives as the item: the submitted fields, the two keys
	// the boundary adds, and nothing else.
	const item = outputItem(submitted, 'form');
	expect(item).toMatchObject({ Name: 'Ada', formMode: 'production' });
	expect(Object.keys(item).sort()).toEqual(['Name', 'formMode', 'submittedAt']);
	expect(outputItem(submitted, 'out')).toMatchObject({ Name: 'Ada', v: 'form-ok' });
}
