import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures';
import { readFocusRing, ringSignature, type RingReading } from '../helpers/focus-ring';
import { createWorkflow, manualWorkflowDocument } from '../helpers/seed';

// BUG-ywbvfa criteria 1 and 2. A keyboard user tabbing through the workflows
// list, a dialog, the editor toolbar and the inspector sees ONE focus style:
// 30 consecutive stops are read and their outline style, width and colour
// (plus whether an old shadcn ring shadow is drawn) must form exactly one
// combination, and every ring must reach 3:1 against the surface it sits on.
// Before the fix the same walk found three (2px outlines, 3px half-opacity
// box-shadow rings and the browser's 1px auto outline).
//
// The test asserts uniformity, not which controls it lands on, so a control
// another change adds to the path only shifts the segments.

const THEMES = ['dark', 'light'] as const;

/** Forces the theme on <html> before the app hydrates (the toggle is FEAT-a3dwj2). */
async function forceTheme(page: Page, theme: (typeof THEMES)[number]): Promise<void> {
	await page.addInitScript((value) => {
		const apply = () => {
			if (!document.documentElement) return false;
			document.documentElement.setAttribute('data-theme', value);
			return true;
		};
		if (apply()) return;
		const observer = new MutationObserver(() => {
			if (apply()) observer.disconnect();
		});
		observer.observe(document, { childList: true, subtree: true });
	}, theme);
}

interface Stop extends RingReading {
	segment: string;
}

/**
 * Presses Tab `count` times and reads the ring after each press. Problems are
 * collected, not thrown, so one run prints the whole walk.
 */
async function walk(page: Page, segment: string, count: number, stops: Stop[], problems: string[]): Promise<void> {
	let previous = await page.evaluate(() => document.activeElement?.outerHTML ?? '');
	for (let i = 0; i < count; i += 1) {
		await page.keyboard.press('Tab');
		const current = await page.evaluate(() => document.activeElement?.outerHTML ?? '');
		if (current === previous) problems.push(`${segment}: Tab ${i + 1} did not move focus`);
		previous = current;
		const reading = await readFocusRing(page);
		if (!reading) {
			problems.push(`${segment}: Tab ${i + 1} left focus on the page body`);
			continue;
		}
		if (reading.clippedBy) problems.push(`${segment}: Tab ${i + 1} ${reading.element} ring is clipped by ${reading.clippedBy}`);
		stops.push({ segment, ...reading });
	}
}

for (const theme of THEMES) {
	test(`one focus style over 30 Tab stops (${theme})`, async ({ page, server }) => {
		await createWorkflow(server.baseURL, manualWorkflowDocument('Lead router'));
		await createWorkflow(server.baseURL, manualWorkflowDocument('Nightly sync'));
		const editorWorkflow = await createWorkflow(server.baseURL, {
			schemaVersion: 1,
			name: 'Order intake',
			nodes: [
				{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
				{ id: 'set', name: 'Set fields', type: 'kilasflow.set', typeVersion: 1, position: { x: 240, y: 0 }, parameters: { assignments: { v: 'x' } } },
				{
					id: 'if',
					name: 'If',
					type: 'kilasflow.if',
					typeVersion: 1,
					position: { x: 480, y: 0 },
					parameters: { conditions: [{ field: 'v', operator: 'equals', value: 'x' }] }
				}
			],
			connections: [
				{ id: 'c1', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'set', port: 'main' } },
				{ id: 'c2', kind: 'main', source: { nodeId: 'set', port: 'main' }, target: { nodeId: 'if', port: 'main' } }
			],
			settings: {}
		});
		await forceTheme(page, theme);
		// The ring is drawn instantly, but colour transitions on the controls
		// would make a background read land mid-animation.
		await page.addInitScript(() => {
			document.addEventListener('DOMContentLoaded', () => {
				const style = document.createElement('style');
				style.textContent = '*,*::before,*::after{transition:none!important;animation:none!important}';
				document.head.appendChild(style);
			});
		});
		const stops: Stop[] = [];
		const problems: string[] = [];

		// 1. The workflows list, from the top of the page.
		await page.goto(`${server.baseURL}/app/workflows`);
		await expect(page.getByText('Order intake').first()).toBeVisible();
		expect(await page.evaluate(() => document.documentElement.getAttribute('data-theme'))).toBe(theme);
		await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
		await walk(page, 'workflows list', 12, stops, problems);

		// 2. A dialog, opened the way a pointer user opens it.
		await page.getByRole('button', { name: 'Import n8n' }).first().click();
		await expect(page.getByRole('dialog')).toContainText('Import from n8n');
		await walk(page, 'import dialog', 5, stops, problems);
		await page.keyboard.press('Escape');
		await expect(page.getByRole('dialog')).toBeHidden();

		// 3. The editor toolbar, from its first button. Six enabled stops follow
		// "Add step" on a saved workflow; a seventh Tab enters the canvas, whose
		// edges show focus by stroke (canvas chrome is FEAT-xj5tv6's).
		await page.goto(`${server.baseURL}/app/workflows/${editorWorkflow.id}`);
		const addStep = page.getByRole('button', { name: 'Add step' });
		await expect(addStep).toBeVisible();
		await expect(page.locator('.svelte-flow__node')).toHaveCount(3);
		await addStep.focus();
		await walk(page, 'editor toolbar', 6, stops, problems);

		// 4. The inspector of a selected node, from its first field.
		await page.keyboard.press('Escape');
		await page.locator('.svelte-flow__node').filter({ hasText: 'Set fields' }).first().click();
		const inspector = page.getByRole('region', { name: 'Set fields properties' });
		await expect(inspector).toBeVisible();
		await inspector.getByRole('textbox').first().focus();
		await walk(page, 'inspector', 7, stops, problems);

		// 5. Outside the 30: the node picker's search field, a composite field on
		// the top edge of an overflow-hidden dialog. Its ring must be whole.
		await page.keyboard.press('Escape');
		await page.getByRole('button', { name: 'Add step' }).click();
		const search = page.getByRole('combobox', { name: /search/i });
		await expect(search).toBeFocused();
		const picker = await readFocusRing(page);
		expect(picker, 'the picker search shows a ring').not.toBeNull();
		if (picker) {
			if (picker.clippedBy) problems.push(`node picker: ${picker.element} ring is clipped by ${picker.clippedBy}`);
			if (ringSignature(picker) !== ringSignature(stops[0])) problems.push(`node picker: ${ringSignature(picker)} differs`);
			if (picker.surfaceContrast < 3) problems.push(`node picker: ring contrast ${picker.surfaceContrast.toFixed(2)}`);
		}

		const table = stops.map((s, i) => `${i + 1}. [${s.segment}] ${s.element} → ${ringSignature(s)} offset ${s.outlineOffset}px, ${s.surfaceContrast.toFixed(2)}:1`).join('\n');
		const combinations = new Set(stops.map(ringSignature));
		expect([...combinations], table).toHaveLength(1);
		expect(problems, table).toEqual([]);
		expect(stops).toHaveLength(30);
		const [only] = [...combinations];
		expect(only.startsWith('solid|2px|'), table).toBe(true);
		expect(only.endsWith('|no-ring-shadow'), table).toBe(true);
		expect(stops.filter((s) => s.surfaceContrast < 3).map((s) => s.element), table).toEqual([]);
	});
}
