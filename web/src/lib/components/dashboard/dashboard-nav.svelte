<script lang="ts">
	import { page } from '$app/state';
	import Activity from '@lucide/svelte/icons/activity';
	import GitBranch from '@lucide/svelte/icons/git-branch';
	import KeyRound from '@lucide/svelte/icons/key-round';
	import Settings from '@lucide/svelte/icons/settings';

	let { onNavigate }: { onNavigate?: () => void } = $props();

	const items = [
		{ href: '/app/workflows', label: 'Workflows', icon: GitBranch },
		{ href: '/executions', label: 'Executions', icon: Activity },
		{ href: '/credentials', label: 'Credentials', icon: KeyRound },
		{ href: '/settings', label: 'Settings', icon: Settings }
	];

	function active(path: string): boolean {
		return page.url.pathname === path || page.url.pathname.startsWith(`${path}/`);
	}
</script>

<nav aria-label="Workspace navigation" class="grid gap-1">
	{#each items as item (item.href)}
		{@const current = active(item.href)}
		<a
			href={item.href}
			aria-current={current ? 'page' : undefined}
			onclick={onNavigate}
			class="group flex min-h-10 items-center gap-3 rounded-lg px-3 text-sm font-medium transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
			class:bg-sidebar-accent={current}
			class:text-sidebar-accent-foreground={current}
			class:text-muted-foreground={!current}
			class:hover:bg-sidebar-accent={!current}
			class:hover:text-sidebar-accent-foreground={!current}
		>
			<item.icon aria-hidden="true" class="size-4" />
			{item.label}
		</a>
	{/each}
</nav>
