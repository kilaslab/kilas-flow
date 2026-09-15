<script lang="ts">
	import { page } from '$app/state';
import Activity from '@lucide/svelte/icons/activity';
import CalendarClock from '@lucide/svelte/icons/calendar-clock';
import Database from '@lucide/svelte/icons/database';
import GitBranch from '@lucide/svelte/icons/git-branch';
import KeyRound from '@lucide/svelte/icons/key-round';
import Settings from '@lucide/svelte/icons/settings';

	let { onNavigate, collapsed = false }: { onNavigate?: () => void; collapsed?: boolean } = $props();

	const items = [
		{ href: '/app/workflows', label: 'Workflows', icon: GitBranch },
		{ href: '/executions', label: 'Executions', icon: Activity },
		{ href: '/schedules', label: 'Schedules', icon: CalendarClock },
		{ href: '/credentials', label: 'Credentials', icon: KeyRound },
		{ href: '/datastores', label: 'Datastores', icon: Database },
		{ href: '/settings', label: 'Settings', icon: Settings }
	];

	function active(path: string): boolean {
		return page.url.pathname === path || page.url.pathname.startsWith(`${path}/`);
	}
</script>

<nav aria-label="Workspace navigation" class="grid gap-0.5">
	{#each items as item (item.href)}
		{@const current = active(item.href)}
		{@const Icon = item.icon}
		<a
			href={item.href}
			aria-current={current ? 'page' : undefined}
			aria-label={collapsed ? item.label : undefined}
			title={collapsed ? item.label : undefined}
			onclick={onNavigate}
			class="group flex h-8 items-center rounded-md text-[0.8125rem] font-medium transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring {collapsed
				? 'justify-center px-0'
				: 'gap-2.5 px-2'}"
			class:bg-sidebar-accent={current}
			class:text-sidebar-accent-foreground={current}
			class:text-muted-foreground={!current}
			class:hover:bg-sidebar-accent={!current}
			class:hover:text-sidebar-accent-foreground={!current}
		>
			<Icon aria-hidden="true" class={collapsed ? 'size-4' : 'size-3.5'} />
			{#if !collapsed}
				{item.label}
			{/if}
		</a>
	{/each}
</nav>
