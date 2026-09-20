import { test, expect } from '../fixtures';
import { createWorkflow, manualWorkflowDocument, runWorkflow } from '../helpers/seed';

// The dashboard's list surfaces, end to end. The executions page reads its
// listing lazily by cursor, says how much of it is loaded, and keeps
// auto-refreshing the newest page without disturbing the pages the user
// already loaded; the workflows page drains its whole listing and therefore
// counts over all of it.
//
// PAGE mirrors EXECUTIONS_PAGE_LIMIT in
// web/src/lib/dashboard/execution-list.ts: the page asks the API for 50 rows a
// page (twice the server default of 25, half the API maximum of 100), so the
// seeded history is derived from it rather than hard-coded.
const PAGE = 50;

// One link per rendered row. The nav entry is exactly '/executions', so the
// trailing slash keeps it out of the count.
const rowLink = 'a[href^="/executions/"]';

/** One manual workflow plus `count` queued runs of it, seeded over the API. */
async function seedRuns(baseURL: string, count: number): Promise<void> {
	const workflow = await createWorkflow(baseURL, manualWorkflowDocument('E2E Paged History'));
	for (let i = 0; i < count; i += 1) {
		await runWorkflow(baseURL, workflow.id);
	}
}

test('the executions list pages by cursor and says what it holds', async ({ page, server }) => {
	await seedRuns(server.baseURL, PAGE + 10);

	await page.goto(`${server.baseURL}/executions`);
	const links = page.locator(rowLink);

	await expect(page.getByText(`Showing the newest ${PAGE} executions. More are available.`, { exact: true })).toBeVisible();
	await expect(links).toHaveCount(PAGE);

	await page.getByRole('button', { name: 'Load more' }).click();

	await expect(page.getByText(`Showing all ${PAGE + 10} executions.`, { exact: true })).toBeVisible();
	await expect(links).toHaveCount(PAGE + 10);
	await expect(page.getByRole('button', { name: /Load more|Loading/ })).toHaveCount(0);
});

test('auto-refresh keeps working while more pages exist', async ({ page, server }) => {
	await seedRuns(server.baseURL, PAGE + 10);

	await page.goto(`${server.baseURL}/executions`);
	const links = page.locator(rowLink);

	// count() does not auto-wait: without the visibility check first it reads 0
	// while the skeleton is still up. The count is then read rather than
	// assumed, so this fails for the right reason on a tree that loads only one
	// server page.
	await expect(links.first()).toBeVisible();
	const initial = await links.count();
	expect(initial).toBeGreaterThan(0);

	// One more run over the API: the poll must pick it up even though the list
	// still has a cursor (the tenant holds more runs than one page).
	const workflow = await createWorkflow(server.baseURL, manualWorkflowDocument('E2E Poll Target'));
	await runWorkflow(server.baseURL, workflow.id);

	await expect(links).toHaveCount(initial + 1, { timeout: 20_000 });
	await expect(page.getByRole('button', { name: 'Load more' })).toBeVisible();
});

test('auto-refresh does not cut a list that was loaded to its end', async ({ page, server }) => {
	await seedRuns(server.baseURL, PAGE + 10);

	await page.goto(`${server.baseURL}/executions`);
	const links = page.locator(rowLink);

	// The list is loaded to its end only when the page says so: the loop runs
	// until the completion line is on screen, which is the one string the page
	// renders only while it holds every row. The absence of the button is not
	// that signal — it is also absent before the first page has painted
	// (isVisible()/count() do not auto-wait) and after a "Load more" failed,
	// where the page swaps it for a retry notice. The first ended the loop with
	// nothing loaded, the second with 50 of 60 rows — the flake the delta
	// review caught — and both then failed on the count below.
	const more = page.getByRole('button', { name: 'Load more' });
	const pager = page.getByRole('button', { name: /Load more|Loading/ });
	const retryMore = page.getByRole('button', { name: 'Try again' });
	const complete = page.getByText(`Showing all ${PAGE + 10} executions.`, { exact: true });

	// The first page must be on screen before the loop starts, so a pre-paint
	// read can never be mistaken for the end of the list.
	await expect(more).toBeVisible();
	await expect(async () => {
		if (await more.isVisible()) await more.click();
		// A page that failed leaves its own retry affordance behind. Pressing
		// it is the page's recovery, not a loosened assertion: a second page
		// that truly cannot be fetched keeps this loop red until toPass gives
		// up, and the count below is still read from the rendered rows.
		if (await retryMore.isVisible()) await retryMore.click();
		await expect(complete).toBeVisible({ timeout: 1_000 });
	}).toPass({ timeout: 30_000 });

	const total = await links.count();
	expect(total).toBe(PAGE + 10);

	// With the list loaded to its end a poll must not shrink it back to the
	// first server page: the new run joins the top and the older rows stay.
	const workflow = await createWorkflow(server.baseURL, manualWorkflowDocument('E2E Complete Target'));
	await runWorkflow(server.baseURL, workflow.id);

	await expect(links).toHaveCount(total + 1, { timeout: 20_000 });
	await expect(pager).toHaveCount(0);
});

test('auto-refresh pauses while the tab is hidden and resumes when it is shown', async ({ page, server }) => {
	await seedRuns(server.baseURL, 3);

	await page.goto(`${server.baseURL}/executions`);
	const links = page.locator(rowLink);
	await expect(links).toHaveCount(3);

	// Headless Chromium cannot background a tab, so the page's own view of
	// visibility is stubbed: the same document.hidden read and the same
	// visibilitychange event the browser would send.
	const setHidden = (hidden: boolean) =>
		page.evaluate((value: boolean) => {
			Object.defineProperty(document, 'hidden', { configurable: true, get: () => value });
			document.dispatchEvent(new Event('visibilitychange'));
		}, hidden);

	await setHidden(true);

	const workflow = await createWorkflow(server.baseURL, manualWorkflowDocument('E2E Hidden Target'));
	await runWorkflow(server.baseURL, workflow.id);

	// Proving a timer did NOT fire can only be done by letting its 5 s interval
	// pass: a deliberate exception to this suite's no-fixed-sleep convention.
	await page.waitForTimeout(6_500);
	await expect(links).toHaveCount(3);

	// Showing the tab again must re-arm the poll: on a tree that read
	// document.hidden non-reactively, polling is dead for good by now.
	await setHidden(false);
	await expect(links).toHaveCount(4, { timeout: 20_000 });
});

test('auto-refresh keeps running after a poll that finds nothing new', async ({ page, server }) => {
	await seedRuns(server.baseURL, 3);

	await page.goto(`${server.baseURL}/executions`);
	const links = page.locator(rowLink);
	await expect(links).toHaveCount(3);

	// Two full poll intervals with nothing new. The chain survives a quiet poll
	// only because refreshHead's `finally` bumps `pollTurn`, which the poll
	// `effect` reads: with that read gone the effect arms one timer, the empty
	// poll changes nothing else the effect depends on, and auto-refresh stops
	// for good. Proving the chain re-armed can only be done by letting the real
	// 5 s intervals elapse — the same deliberate exception to this suite's
	// no-fixed-sleep convention the hidden-tab test above makes.
	await page.waitForTimeout(12_000);
	await expect(links).toHaveCount(3);

	// A run created after those idle polls must still reach the top: only a
	// re-armed poll can pick it up, so this fails when the chain is broken.
	const workflow = await createWorkflow(server.baseURL, manualWorkflowDocument('E2E Idle Poll Target'));
	await runWorkflow(server.baseURL, workflow.id);

	await expect(links).toHaveCount(4, { timeout: 8_000 });
});

test('the workflows heading counts the workspace and the list header counts the search', async ({ page, server }) => {
	for (const name of ['Alpha flow', 'Beta flow', 'Gamma flow']) {
		await createWorkflow(server.baseURL, manualWorkflowDocument(name));
	}

	await page.goto(`${server.baseURL}/app/workflows`);

	// The heading states the workspace total, and must keep stating it while a
	// search narrows the list: printing the filtered count under "in this
	// workspace" made a search look like the workspace itself had shrunk.
	await expect(page.getByText('3 in this workspace', { exact: true })).toBeVisible();

	await page.getByRole('searchbox', { name: 'Search' }).fill('Alpha');

	await expect(page.getByText('3 in this workspace', { exact: true })).toBeVisible();
	// The list header is where the narrowed count belongs, as shown-of-total.
	// Three workflows fit one page, so the bottom pager does not render and
	// this string is unique on the page.
	await expect(page.getByText('1 of 3 workflows · page 1 of 1', { exact: true })).toBeVisible();
});
