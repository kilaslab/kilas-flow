import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

/**
 * A dashboard section is declared twice, in two files that know nothing
 * about each other: the `items` array in `dashboard-nav.svelte` drives the
 * sidebar and the mobile sheet, while the `sectionTitle` derivation in
 * `(dashboard)/+layout.svelte` names the header. A route registered in only
 * one of the two still renders — with another section's title — so this
 * reads both sources and pairs every entry with the branch that names it.
 */
describe('the dashboard section registration', () => {
	const root = fileURLToPath(new URL('../..', import.meta.url));
	const nav = readFileSync(`${root}/lib/components/dashboard/dashboard-nav.svelte`, 'utf8');
	const layout = readFileSync(`${root}/routes/(dashboard)/+layout.svelte`, 'utf8');

	const entries = [...nav.matchAll(/\{\s*href:\s*'([^']+)'\s*,\s*label:\s*'([^']+)'/g)].map(
		(match) => ({ href: match[1], label: match[2] })
	);
	const returns = [...layout.matchAll(/return '([^']+)'/g)].map((match) => match[1]);
	const fallThrough = returns[returns.length - 1];

	it('finds the sections it is guarding', () => {
		expect(entries.length).toBeGreaterThan(4);
		expect(fallThrough).toBeDefined();
	});

	it.each(entries.map((entry) => [entry.label, entry.href] as const))(
		'names the %s entry in the header',
		(label, href) => {
			if (label === fallThrough) return;
			const firstSegment = `/${href.split('/').filter(Boolean)[0]}`;
			expect(layout).toContain(`startsWith('${firstSegment}')`);
			expect(layout).toContain(`return '${label}'`);
		}
	);
});
