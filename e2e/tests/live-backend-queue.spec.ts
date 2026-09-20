// Live queue and trigger suite (EPIC-87t47t, FEAT-adyeh0): a manual run queues
// and executes with a live event feed, a parked wait is cancelled, the real
// scheduler fires an activated cron workflow, and a timed wait resumes by
// itself. The scheduler tick is a fixed 15 seconds (internal/scheduler), so
// every wall-clock expectation here is a poll with a budget, never a sleep.
import { test, expect } from '../fixtures';
import { createWorkflow, readExecutionEvents, runWorkflow, waitForExecution } from '../helpers/seed';
import {
	liveApi,
	waitForTriggerExecution,
	type ExecutionPage,
	type ExecutionRecord,
	type WorkflowResource
} from '../fixtures/live-backend';
import { uniqueName } from '../fixtures/datastore';

type JsonObject = Record<string, unknown>;

function specNode(id: string, name: string, type: string, parameters?: JsonObject): JsonObject {
	return { id, name, type, typeVersion: 1, position: { x: 0, y: 0 }, ...(parameters ? { parameters } : {}) };
}

function specConn(id: string, source: string, target: string): JsonObject {
	return { id, kind: 'main', source: { nodeId: source, port: 'main' }, target: { nodeId: target, port: 'main' } };
}

// readWaitResumeValue reads the wait node's own resume value from the served
// node catalogue, so the document is built from the definition the server
// actually validates rather than a literal copied from Go.
async function readWaitResumeValue(baseURL: string): Promise<string> {
	const definitions = await liveApi<Array<{ type: string; parameters?: Array<{ key: string; default?: unknown }> }>>(
		baseURL,
		'GET',
		'/node-types'
	);
	const wait = definitions.find((definition) => definition.type === 'kilasflow.wait');
	const resume = wait?.parameters?.find((property) => property.key === 'resume')?.default;
	expect(typeof resume, 'kilasflow.wait serves a resume default').toBe('string');
	return resume as string;
}

function manualWorkflow(name: string, nodes: JsonObject[], connections: JsonObject[]): JsonObject {
	return {
		schemaVersion: 1,
		name,
		nodes: [specNode('manual', 'Manual Trigger', 'kilasflow.manual'), ...nodes],
		connections,
		settings: {}
	};
}

test('a manual run queues, executes, feeds events, and pages', async ({ server }) => {
	const baseURL = server.baseURL;
	const workflow = await createWorkflow(
		baseURL,
		manualWorkflow(
			uniqueName('live queue'),
			[specNode('set', 'Set', 'kilasflow.set', { assignments: { tick: 'ran' } })],
			[specConn('c1', 'manual', 'set')]
		)
	);

	const run = await liveApi<{ id: string; status: string }>(
		baseURL,
		'POST',
		`/workflows/${workflow.id}/run`,
		{},
		202
	);
	expect(run.status).toBe('queued');
	const record = await waitForExecution(baseURL, run.id);
	expect(record.status).toBe('succeeded');
	const events = await readExecutionEvents(baseURL, run.id);
	expect(events.map((event) => event.type)).toContain('execution.completed');

	// Two more runs give the listing a page boundary.
	await liveApi(baseURL, 'POST', `/workflows/${workflow.id}/run`, {}, 202);
	await liveApi(baseURL, 'POST', `/workflows/${workflow.id}/run`, {}, 202);
	await expect
		.poll(
			async () =>
				(await liveApi<ExecutionPage>(baseURL, 'GET', `/executions?workflowId=${workflow.id}&limit=25`)).items.length,
			{ message: 'all three queued runs are recorded' }
		)
		.toBe(3);

	const firstPage = await liveApi<ExecutionPage>(baseURL, 'GET', `/executions?workflowId=${workflow.id}&limit=2`);
	expect(firstPage.items.length).toBe(2);
	expect(firstPage.nextCursor).toBeTruthy();
	const secondPage = await liveApi<ExecutionPage>(
		baseURL,
		'GET',
		`/executions?workflowId=${workflow.id}&limit=2&cursor=${encodeURIComponent(firstPage.nextCursor!)}`
	);
	expect(secondPage.items.length).toBe(1);
	const firstIds = new Set(firstPage.items.map((item) => item.id));
	expect(secondPage.items.some((item) => firstIds.has(item.id))).toBe(false);

	// A node that cannot start a run is refused at the edge, not queued.
	await liveApi(baseURL, 'POST', `/workflows/${workflow.id}/run`, { triggerNodeId: 'nope' }, 422);
});

test('a parked execution accepts a cancellation and reports it on the feed', async ({ server }) => {
	const baseURL = server.baseURL;
	const resume = await readWaitResumeValue(baseURL);
	const workflow = await createWorkflow(
		baseURL,
		manualWorkflow(
			uniqueName('live cancel'),
			[
				specNode('wait', 'Wait', 'kilasflow.wait', { resume, amount: 30, unit: 'seconds' }),
				specNode('set', 'Set', 'kilasflow.set', { assignments: { after: 'wait' } })
			],
			[specConn('c1', 'manual', 'wait'), specConn('c2', 'wait', 'set')]
		)
	);

	const runId = await runWorkflow(baseURL, workflow.id);
	await expect
		.poll(
			async () => {
				const execution = await liveApi<ExecutionRecord>(baseURL, 'GET', `/executions/${runId}`);
				return execution.status === 'waiting' || Boolean(execution.resumeUrl);
			},
			{ message: 'the wait parks the execution', timeout: 15_000 }
		)
		.toBe(true);

	await liveApi(baseURL, 'POST', `/executions/${runId}/cancel`, {}, 202);
	const cancelled = await waitForExecution(baseURL, runId);
	expect(cancelled.status).toBe('cancelled');
	const events = await readExecutionEvents(baseURL, runId);
	expect(events.map((event) => event.type)).toContain('execution.cancelled');
});

test('an activated schedule fires on the scheduler tick, and the schedule rows are manageable over REST', async ({ server }) => {
	const baseURL = server.baseURL;
	const workflow = await createWorkflow(baseURL, {
		schemaVersion: 1,
		name: uniqueName('live schedule'),
		nodes: [
			specNode('schedule', 'Schedule', 'kilasflow.schedule', { cron: '*/20 * * * * *' }),
			specNode('set', 'Set', 'kilasflow.set', { assignments: { tick: 'fired' } })
		],
		connections: [specConn('c1', 'schedule', 'set')],
		settings: {}
	});

	const activated = await liveApi<WorkflowResource>(baseURL, 'POST', `/workflows/${workflow.id}/activate`);
	expect(activated.active).toBe(true);

	// Activation writes the schedule row the scheduler reads; without it the
	// workflow would activate cleanly and never run.
	const schedules = await liveApi<Array<{ id: string; workflowId: string; cron: string }>>(baseURL, 'GET', '/schedules');
	const firing = schedules.find((schedule) => schedule.workflowId === workflow.id);
	expect(firing, `no schedule row for ${workflow.id}`).toBeTruthy();
	expect(firing!.cron).toContain('*/20');

	// The tick is 15 seconds and the expression fires every 20, so the budget
	// is the scheduler's own cadence, not a guess.
	const record = await waitForTriggerExecution(baseURL, workflow.id, 'schedule', [], 90_000);
	expect(record.status).toBe('succeeded');
	expect(record.triggerNodeId).toBe('schedule');
	const triggerRun = record.nodeRuns?.find((node) => node.nodeId === 'schedule');
	expect(triggerRun?.status).toBe('succeeded');
	// The item carries the schedule it fired from and its due instant.
	expect(JSON.stringify(triggerRun?.output)).toContain('"scheduleId"');
	expect(JSON.stringify(triggerRun?.output)).toContain('"timestamp"');

	// The REST surface beside the trigger: an inactive schedule has no due
	// time, activating it computes one, deleting it removes the row.
	const created = await liveApi<{ id: string; active: boolean; nextRunAt?: string | null }>(
		baseURL,
		'POST',
		'/schedules',
		{ workflowId: workflow.id, cron: '0 9 * * *', active: false },
		201
	);
	expect(created.nextRunAt ?? null).toBeNull();
	const switchedOn = await liveApi<{ active: boolean; nextRunAt?: string | null }>(
		baseURL,
		'PUT',
		`/schedules/${created.id}`,
		{ workflowId: workflow.id, cron: '0 9 * * *', active: true }
	);
	expect(switchedOn.active).toBe(true);
	expect(switchedOn.nextRunAt ?? null).not.toBeNull();
	await liveApi(baseURL, 'DELETE', `/schedules/${created.id}`, undefined, 204);
});

test('a timed wait resumes by itself once its interval elapses', async ({ server }) => {
	const baseURL = server.baseURL;
	const resume = await readWaitResumeValue(baseURL);
	const workflow = await createWorkflow(
		baseURL,
		manualWorkflow(
			uniqueName('live wait timer'),
			[
				specNode('wait', 'Wait', 'kilasflow.wait', { resume, amount: 2, unit: 'seconds' }),
				specNode('set', 'Set', 'kilasflow.set', { assignments: { after: 'wait' } })
			],
			[specConn('c1', 'manual', 'wait'), specConn('c2', 'wait', 'set')]
		)
	);

	const startedAt = Date.now();
	const runId = await runWorkflow(baseURL, workflow.id);
	const record = await waitForExecution(baseURL, runId);
	expect(record.status).toBe('succeeded');
	expect(Date.now() - startedAt).toBeGreaterThanOrEqual(2_000);
	const duration = new Date(record.finishedAt as string).getTime() - new Date(record.startedAt as string).getTime();
	expect(duration).toBeGreaterThanOrEqual(2_000);
});
