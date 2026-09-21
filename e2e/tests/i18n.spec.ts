import { test, expect } from '../fixtures';
import { createEmbedSession, createWorkflow, manualWorkflowDocument } from '../helpers/seed';

// The i18n proof: the second locale, end to end, on the two surfaces the
// ticket names — the dashboard editor a team works in, and the embedded editor
// a customer's user sees. Both assertions are about what a reader can see
// (the document language and a control's own label), not about a catalog file
// existing, which web/src/lib/i18n/catalog.test.ts already covers.

test('the dashboard editor renders a second locale', async ({ page, server }) => {
	const workflow = await createWorkflow(server.baseURL, manualWorkflowDocument('E2E Locale Workflow'));

	await page.goto(`${server.baseURL}/app/workflows`);
	// The sidebar switcher, not the header: (dashboard)/+layout.svelte hides the
	// header at `lg`, and the sidebar is the reachable control.
	await page.getByLabel('Language').selectOption('id');

	await expect(page.locator('html')).toHaveAttribute('lang', 'id');
	await expect(page.getByRole('link', { name: 'Alur kerja', exact: true })).toBeVisible();

	// A fresh navigation, so this also proves the choice survives a reload —
	// and that the editor itself, not only the shell, is localized: the Save
	// control belongs to the editor's own toolbar.
	await page.goto(`${server.baseURL}/app/workflows/${workflow.id}`);
	await expect(page.locator('html')).toHaveAttribute('lang', 'id');
	await expect(page.getByLabel('Simpan', { exact: true })).toBeVisible();
});

test('the embedded editor follows the locale its host sends', async ({ page, server, stub }) => {
	const workflow = await createWorkflow(server.baseURL, manualWorkflowDocument('E2E Embed Locale Workflow'));
	const session = await createEmbedSession(server.baseURL, { workflowId: workflow.id, origin: stub.origin });

	const hostURL =
		`${stub.url('/host')}?server=${encodeURIComponent(server.baseURL)}` +
		`&workflow=${encodeURIComponent(workflow.id)}` +
		`&token=${encodeURIComponent(session.token)}` +
		`&scopes=${encodeURIComponent(session.scopes.join(','))}` +
		`&locale=id`;
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

	// A fresh Playwright context starts with empty localStorage, so the frame
	// can only be in Indonesian because the session carried the locale: the
	// stored preference is the one other source it could have read.
	const frame = page.frameLocator('#e2e-frame');
	await expect(frame.locator('html')).toHaveAttribute('lang', 'id');
	await expect(frame.getByLabel('Simpan', { exact: true })).toBeVisible();
});
