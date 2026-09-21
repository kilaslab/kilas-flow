import { describe, expect, it } from 'vitest';

import { dashboardSections, sectionForPath, sectionLabel } from '$lib/dashboard/nav-sections';

/**
 * The section registration used to live twice — an `items` array in
 * dashboard-nav.svelte drove the sidebar and a `sectionTitle` derivation in
 * (dashboard)/+layout.svelte named the header — and the test that guarded it
 * regex-parsed both sources to check the two hand-maintained lists agreed.
 * They now read one registration, so what is left to prove is the mapping
 * itself.
 */
describe('the dashboard section registration', () => {
	it('maps every declared href to its own section', () => {
		for (const section of dashboardSections) {
			expect(sectionForPath(section.href), section.href).toBe(section.key);
		}
	});

	it('maps a nested path to its parent section', () => {
		expect(sectionForPath('/executions/42')).toBe('executions');
		expect(sectionForPath('/app/workflows/draft-1/versions/3')).toBe('workflows');
	});

	it('maps an unknown path to the default section', () => {
		expect(sectionForPath('/nowhere')).toBe('workflows');
		expect(sectionForPath('/')).toBe('workflows');
	});

	it('never declares the same href or label twice', () => {
		expect(new Set(dashboardSections.map((section) => section.href)).size).toBe(dashboardSections.length);
		const labels = dashboardSections.map((section) => sectionLabel(section.key));
		expect(labels.every((label) => label.length > 0)).toBe(true);
		expect(new Set(labels).size).toBe(labels.length);
	});
});
