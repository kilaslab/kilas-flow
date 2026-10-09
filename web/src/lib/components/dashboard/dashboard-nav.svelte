<script lang="ts">
	import type { Component } from 'svelte';
	import { page } from '$app/state';
	import Activity from '@lucide/svelte/icons/activity';
	import CalendarClock from '@lucide/svelte/icons/calendar-clock';
	import Database from '@lucide/svelte/icons/database';
	import GitBranch from '@lucide/svelte/icons/git-branch';
	import KeyRound from '@lucide/svelte/icons/key-round';
	import Settings from '@lucide/svelte/icons/settings';

	import * as m from '$lib/paraglide/messages.js';
	import { dashboardSections, sectionLabel, type SectionKey } from '$lib/dashboard/nav-sections';

	// `path` decides which section is current; it defaults to the page's own
	// path (the design-system state matrix passes one to show a current item).
	let { onNavigate, collapsed = false, path }: { onNavigate?: () => void; collapsed?: boolean; path?: string } = $props();

	// Icons are components, so they stay beside the markup that renders them.
	// Everything a reader sees — the hrefs and the labels — comes from the
	// section registration above, which the header title reads too.
	const ICONS: Record<SectionKey, Component> = {
		workflows: GitBranch,
		executions: Activity,
		schedules: CalendarClock,
		credentials: KeyRound,
		datastores: Database,
		settings: Settings
	};

	function active(href: string): boolean {
		const current = path ?? page.url.pathname;
		return current === href || current.startsWith(`${href}/`);
	}
</script>

<nav aria-label={m.common_workspace_navigation()} class="grid gap-0.5">
	{#each dashboardSections as section (section.href)}
		{@const current = active(section.href)}
		{@const Icon = ICONS[section.key]}
		{@const label = sectionLabel(section.key)}
		<a
			href={section.href}
			aria-current={current ? 'page' : undefined}
			aria-label={collapsed ? label : undefined}
			title={collapsed ? label : undefined}
			onclick={onNavigate}
			class="group flex h-8 items-center rounded-md text-[0.8125rem] font-medium transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring {collapsed
				? 'justify-center px-0'
				: 'gap-2.5 px-2'}"
			class:bg-sidebar-accent={current}
			class:text-sidebar-accent-foreground={current}
			class:text-muted-foreground={!current}
			class:hover:bg-accent={!current}
			class:active:bg-sidebar-accent={!current}
			class:hover:text-sidebar-accent-foreground={!current}
		>
			<Icon aria-hidden="true" class={collapsed ? 'size-4' : 'size-3.5'} />
			{#if !collapsed}
				{label}
			{/if}
		</a>
	{/each}
</nav>
