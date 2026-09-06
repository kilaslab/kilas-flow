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
