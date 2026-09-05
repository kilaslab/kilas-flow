<script lang="ts">
	import { page } from '$app/state';
	import Activity from '@lucide/svelte/icons/activity';
	import GitBranch from '@lucide/svelte/icons/git-branch';
	import Settings from '@lucide/svelte/icons/settings';

	let { onNavigate }: { onNavigate?: () => void } = $props();

	function active(path: string): boolean {
		return page.url.pathname === path || page.url.pathname.startsWith(`${path}/`);
	}
</script>

<nav aria-label="Workspace navigation" class="grid gap-1">
	<a
		href="/app/workflows"
		aria-current={active('/app/workflows') ? 'page' : undefined}
		onclick={onNavigate}
		class="group flex min-h-10 items-center gap-3 rounded-lg px-3 text-sm font-medium transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
		class:bg-sidebar-accent={active('/app/workflows')}
		class:text-sidebar-accent-foreground={active('/app/workflows')}
		class:text-muted-foreground={!active('/app/workflows')}
		class:hover:bg-sidebar-accent={!active('/app/workflows')}
		class:hover:text-sidebar-accent-foreground={!active('/app/workflows')}
	>
		<GitBranch aria-hidden="true" class="size-4" />
		Workflows
	</a>
	<a
		href="/executions"
		aria-current={active('/executions') ? 'page' : undefined}
		onclick={onNavigate}
		class="group flex min-h-10 items-center gap-3 rounded-lg px-3 text-sm font-medium transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
		class:bg-sidebar-accent={active('/executions')}
		class:text-sidebar-accent-foreground={active('/executions')}
		class:text-muted-foreground={!active('/executions')}
		class:hover:bg-sidebar-accent={!active('/executions')}
		class:hover:text-sidebar-accent-foreground={!active('/executions')}
	>
		<Activity aria-hidden="true" class="size-4" />
		Executions
	</a>
	<a
		href="/settings"
		aria-current={active('/settings') ? 'page' : undefined}
		onclick={onNavigate}
		class="group flex min-h-10 items-center gap-3 rounded-lg px-3 text-sm font-medium transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
		class:bg-sidebar-accent={active('/settings')}
		class:text-sidebar-accent-foreground={active('/settings')}
		class:text-muted-foreground={!active('/settings')}
		class:hover:bg-sidebar-accent={!active('/settings')}
		class:hover:text-sidebar-accent-foreground={!active('/settings')}
	>
		<Settings aria-hidden="true" class="size-4" />
		Settings
	</a>
</nav>
