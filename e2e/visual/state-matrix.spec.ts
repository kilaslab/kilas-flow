import { forceStates } from './force-state';
import { expect, test } from './fixtures';

// BUG-ywbvfa criterion 3: every primitive's default, hover, pressed,
// focus-visible and disabled states, captured per theme (the projects).
// The harness is web/src/lib/design-system/state-matrix.svelte.

const SECTIONS = ['button', 'icon-button', 'input', 'select', 'switch', 'tab', 'menu-item', 'list-row', 'canvas-node'];

for (const section of SECTIONS) {
	test(`state matrix: ${section}`, async ({ page, stable }) => {
		await stable.goto(`/design/states?section=${section}`);
		await expect(page.getByTestId('matrix')).toBeVisible();
		if (section === 'canvas-node') await expect(page.locator('.svelte-flow__node .kf-node-tile')).toHaveCount(7);
		if (section === 'menu-item') await expect(page.getByRole('menuitem')).toHaveCount(6);

		const { forced } = await forceStates(page);
		// Every section forces at least hover, pressed and focus-visible cells;
		// zero means the selector or the harness drifted and the shot proves nothing.
		expect(forced).toBeGreaterThanOrEqual(3);

		await expect(page).toHaveScreenshot(`${section}.png`);
	});
}
