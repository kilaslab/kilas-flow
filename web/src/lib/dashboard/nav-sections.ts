import * as m from '$lib/paraglide/messages.js';

/**
 * The dashboard's section registration — the single place that decides which
 * sections exist. It used to exist twice, as an `items` array in
 * dashboard-nav.svelte and as a chain of `startsWith` branches in
 * (dashboard)/+layout.svelte, and a route registered in only one of them still
 * rendered, under another section's title. Both now read this module.
 */
export type SectionKey =
	| 'workflows'
	| 'executions'
	| 'schedules'
	| 'credentials'
	| 'datastores'
	| 'settings';

/** Order is the order the sidebar and the mobile sheet render. */
export const dashboardSections: { href: string; key: SectionKey }[] = [
	{ href: '/app/workflows', key: 'workflows' },
	{ href: '/executions', key: 'executions' },
	{ href: '/schedules', key: 'schedules' },
	{ href: '/credentials', key: 'credentials' },
	{ href: '/datastores', key: 'datastores' },
	{ href: '/settings', key: 'settings' }
];

/**
 * Written out rather than looked up by a composed key: a declared reference is
 * what keeps the tree-shaker able to drop the messages a build never reaches.
 */
const SECTION_LABELS: Record<SectionKey, () => string> = {
	workflows: m.nav_workflows,
	executions: m.nav_executions,
	schedules: m.nav_schedules,
	credentials: m.nav_credentials,
	datastores: m.nav_datastores,
	settings: m.nav_settings
};

export function sectionLabel(key: SectionKey): string {
	return SECTION_LABELS[key]();
}

/**
 * The section a pathname belongs to: the longest declared href that is either
 * the pathname itself or a parent of it, so `/executions/42` is still
 * executions and `/settings-archive` is not settings. Anything else — an
 * unregistered route, the dashboard root — falls back to the first section,
 * which is also the one whose href is the app's landing route.
 */
export function sectionForPath(pathname: string): SectionKey {
	let match: { href: string; key: SectionKey } | null = null;
	for (const section of dashboardSections) {
		if (pathname !== section.href && !pathname.startsWith(`${section.href}/`)) continue;
		if (!match || section.href.length > match.href.length) match = section;
	}
	return match ? match.key : 'workflows';
}
