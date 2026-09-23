import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures';
import {
	createCredential,
	createEmbedSession,
	createSchedule,
	createWorkflow,
	httpWorkflowDocument,
	listSchedules,
	manualWorkflowDocument,
	readExecutionEvents,
	runWorkflow,
	waitForExecution
} from '../helpers/seed';

// The harness smoke proof: one spec exercising the whole composition — real
// binary, real database, real SPA — over dashboard and embed surfaces. The
// suites that build on this harness (V2-p11-3 through V2-p11-7) live in their
// own specs; this one proves the fixture chain works end to end.

test('dashboard lists a workflow seeded over the API', async ({ page, server }) => {
	const workflow = await createWorkflow(server.baseURL, manualWorkflowDocument('E2E Smoke Workflow'));

	await page.goto(`${server.baseURL}/app/workflows`);

	// The real SPA, not the "SPA not built" placeholder: the dashboard shell
	// only exists in the embedded build.
	await expect(page.locator('[data-dashboard-shell]')).toBeVisible();
	await expect(page.getByRole('link', { name: /E2E Smoke Workflow/ })).toBeVisible();
	expect(workflow.id.length).toBeGreaterThan(0);
});

test('a workflow reaches the local stub through the outbound policy', async ({ server, stub }) => {
	const workflow = await createWorkflow(server.baseURL, httpWorkflowDocument('E2E Stub Caller', stub.url('/echo')));
	const credential = await createCredential(server.baseURL, {
		name: 'E2E Bearer',
		type: 'httpBearerAuth',
		fields: { token: 'e2e-smoke' }
	});
	expect(credential.id.length).toBeGreaterThan(0);

	const executionId = await runWorkflow(server.baseURL, workflow.id);
	const record = await waitForExecution(server.baseURL, executionId);
	expect(record.status).toBe('succeeded');

	expect(stub.requests.some((request) => request.path === '/echo')).toBe(true);

	// The live feed observed to its terminal event, not assumed from the poll.
	const events = await readExecutionEvents(server.baseURL, executionId);
	expect(events.map((event) => event.type)).toContain('execution.completed');
});

test('a different loopback stays refused by the outbound guard', async ({ server, stub }) => {
	// Same port, different loopback address: neither the allowed_hosts entry
	// (127.0.0.1) nor the exact host:port endpoint exemption covers it, while
	// allow_private_networks stays off.
	const refusedURL = `http://127.0.0.2:${stub.port}/refused`;
	const workflow = await createWorkflow(server.baseURL, httpWorkflowDocument('E2E Refused Caller', refusedURL));

	const executionId = await runWorkflow(server.baseURL, workflow.id);
	const record = await waitForExecution(server.baseURL, executionId);
	expect(record.status).toBe('failed');

	expect(stub.requests.some((request) => request.path === '/refused')).toBe(false);
});

test('a seeded schedule is listed back over the API', async ({ server }) => {
	const workflow = await createWorkflow(server.baseURL, manualWorkflowDocument('E2E Scheduled Workflow'));
	const schedule = await createSchedule(server.baseURL, {
		workflowId: workflow.id,
		cron: '*/5 * * * *',
		active: false
	});

	const schedules = await listSchedules(server.baseURL);
	expect(schedules.some((candidate) => candidate.id === schedule.id)).toBe(true);
});

test('the embedded editor completes its postMessage handshake', async ({ page, server, stub }) => {
	const workflow = await createWorkflow(server.baseURL, manualWorkflowDocument('E2E Embed Workflow'));
	const session = await createEmbedSession(server.baseURL, { workflowId: workflow.id, origin: stub.origin });

	const hostURL =
		`${stub.url('/host')}?server=${encodeURIComponent(server.baseURL)}` +
		`&workflow=${encodeURIComponent(workflow.id)}` +
		`&token=${encodeURIComponent(session.token)}` +
		`&scopes=${encodeURIComponent(session.scopes.join(','))}`;
	await page.goto(hostURL);

	// The frame announces itself before any token moves, exactly as
	// sdk/src/browser.ts expects from the production host.
	await expect
		.poll(
			async () =>
				page.evaluate(
					() =>
						(window as unknown as { __e2eEvents: { type: string }[] }).__e2eEvents.filter(
							(event) => event.type === 'kilasflow:embed-ready'
						).length
				),
			{ message: 'the embed frame announces readiness' }
		)
		.toBeGreaterThan(0);

	const frame = page.frameLocator('#e2e-frame');
	// Accepted session: the waiting state is gone and no denial took its place.
	await expect(frame.getByText('Waiting for the host application…')).toBeHidden({ timeout: 30_000 });
	await expect(frame.getByRole('alert')).toBeHidden();
});

// What the stub host page recorded from the frame, filtered to one message type.
interface HostEvent {
	origin: string;
	type: string;
	executionId?: string;
	status?: string;
}

async function hostEvents(page: Page, type: string): Promise<HostEvent[]> {
	return page.evaluate(
		(wanted) => (window as unknown as { __e2eEvents: HostEvent[] }).__e2eEvents.filter((event) => event.type === wanted),
		type
	);
}

// BUG-b3p8va. The frame is served by KilasFlow, so its saves and runs carry
// KilasFlow's origin, not the host page's. The server used to compare that with
// the host origin signed into the token and refuse every write, while reads
// worked because a same-origin GET sends no Origin at all. The stub host is on
// another port, so this is the cross-origin shape a real host SaaS application
// has, and the test drives a save and a run the way its user would.
test('the embedded editor saves and runs from a host page on another origin', async ({ page, server, stub }) => {
	const workflow = await createWorkflow(server.baseURL, {
		schemaVersion: 1,
		name: 'E2E Embed Save And Run',
		nodes: [
			{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
			{
				id: 'set1',
				name: 'Set One',
				type: 'kilasflow.set',
				typeVersion: 1,
				position: { x: 240, y: 0 },
				parameters: { assignments: { v: 'before' } }
			}
		],
		connections: [{ id: 'c1', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'set1', port: 'main' } }],
		settings: {}
	});
	const session = await createEmbedSession(server.baseURL, { workflowId: workflow.id, origin: stub.origin });
	expect(new URL(stub.origin).origin).not.toBe(new URL(server.baseURL).origin);

	await page.goto(
		`${stub.url('/host')}?server=${encodeURIComponent(server.baseURL)}` +
			`&workflow=${encodeURIComponent(workflow.id)}` +
			`&token=${encodeURIComponent(session.token)}` +
			`&scopes=${encodeURIComponent(session.scopes.join(','))}`
	);

	const frame = page.frameLocator('#e2e-frame');
	await frame
		.locator('[data-testid="workflow-canvas"] .svelte-flow__node')
		.filter({ hasText: 'Set One' })
		.first()
		.click({ timeout: 30_000 });
	const panel = frame.getByRole('region', { name: 'Set One properties' });
	await expect(panel).toBeVisible();
	await panel.getByRole('textbox', { name: 'Fields to Set field value' }).fill('after');

	const save = frame.getByRole('button', { name: 'Save', exact: true });
	await expect(save).toBeEnabled();
	await save.click();
	await expect
		.poll(async () => (await hostEvents(page, 'kilasflow:workflow-saved')).length, {
			message: 'the frame reports the save to its host'
		})
		.toBeGreaterThan(0);

	await frame.getByTestId('toolbar-run').click();
	await expect
		.poll(async () => (await hostEvents(page, 'kilasflow:execution-finished'))[0]?.status, {
			message: 'the frame reports the run it started finishing',
			timeout: 60_000
		})
		.toBe('succeeded');

	// The run executed the saved edit, not the document the workflow was
	// created with: the save really reached the server.
	const [finished] = await hostEvents(page, 'kilasflow:execution-finished');
	const record = await waitForExecution(server.baseURL, finished.executionId ?? '');
	const setRun = (record.nodeRuns as { nodeId: string; output: { json: unknown }[][] }[]).find((run) => run.nodeId === 'set1');
	expect(setRun?.output[0]?.[0]?.json).toMatchObject({ v: 'after' });
});
