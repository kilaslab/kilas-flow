<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import Menu from '@lucide/svelte/icons/menu';
	import PanelLeftClose from '@lucide/svelte/icons/panel-left-close';
	import PanelLeftOpen from '@lucide/svelte/icons/panel-left-open';

	import DashboardNav from '$lib/components/dashboard/dashboard-nav.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Sheet from '$lib/components/ui/sheet';

	let { children } = $props();
	let mobileNavOpen = $state(false);

	// Collapsed = icons-only rail. Default expanded until the client reports
	// its stored preference, so SSR and first paint never mismatch hydration.
	let sidebarCollapsed = $state(false);

	const STORAGE_KEY = 'kilasflow.sidebar.collapsed';

	onMount(() => {
		try {
			sidebarCollapsed = localStorage.getItem(STORAGE_KEY) === 'true';
		} catch {
			sidebarCollapsed = false;
		}
	});

	function toggleSidebar() {
		sidebarCollapsed = !sidebarCollapsed;
		try {
			localStorage.setItem(STORAGE_KEY, String(sidebarCollapsed));
		} catch {
			// Private mode / blocked storage: the toggle still works for the session.
		}
	}

	const sectionTitle = $derived.by(() => {
		if (page.url.pathname.startsWith('/executions')) return 'Executions';
		if (page.url.pathname.startsWith('/schedules')) return 'Schedules';
		if (page.url.pathname.startsWith('/credentials')) return 'Credentials';
		if (page.url.pathname.startsWith('/datastores')) return 'Datastores';
		if (page.url.pathname.startsWith('/settings')) return 'Settings';
		return 'Workflows';
	});
	const editorRoute = $derived(/^\/app\/workflows\/[^/]+$/.test(page.url.pathname));
</script>

<svelte:head>
	<meta name="theme-color" content="#111a18" />
</svelte:head>

<div
	data-dashboard-shell
	data-sidebar-collapsed={sidebarCollapsed ? 'true' : 'false'}
	class="min-h-dvh bg-background lg:grid {sidebarCollapsed
		? 'lg:grid-cols-[3.5rem_minmax(0,1fr)]'
		: 'lg:grid-cols-[13rem_minmax(0,1fr)]'}"
>
	<aside class="hidden border-r border-sidebar-border bg-sidebar lg:sticky lg:top-0 lg:flex lg:h-dvh lg:flex-col">
		<div
			class="flex border-b border-sidebar-border {sidebarCollapsed
				? 'flex-col items-center gap-1 py-2'
				: 'h-11 flex-row items-center justify-between px-3'}">
			<a
				href="/app/workflows"
				class="flex items-center gap-2 rounded-md focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-sidebar-ring"
				title={sidebarCollapsed ? 'KilasFlow' : undefined}
			>
				<span aria-hidden="true" class="grid size-5 place-items-center rounded bg-sidebar-primary text-[0.625rem] font-bold tracking-tight text-sidebar-primary-foreground">K</span>
				{#if !sidebarCollapsed}
					<span class="text-[0.8125rem] font-semibold tracking-tight text-sidebar-foreground">KilasFlow</span>
				{/if}
			</a>
			<button
				type="button"
				onclick={toggleSidebar}
				aria-expanded={!sidebarCollapsed}
				aria-label={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}
				title={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}
				class="grid size-7 place-items-center rounded-md text-sidebar-foreground/70 transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-sidebar-ring"
			>
				{#if sidebarCollapsed}
					<PanelLeftOpen aria-hidden="true" class="size-4" />
				{:else}
					<PanelLeftClose aria-hidden="true" class="size-4" />
				{/if}
			</button>
		</div>
		<div class="flex-1 {sidebarCollapsed ? 'px-1.5' : 'px-2'} py-2">
			<DashboardNav collapsed={sidebarCollapsed} />
		</div>
	</aside>

	<section class="min-w-0">
		<header class="sticky top-0 z-20 flex h-11 items-center gap-2 border-b border-border bg-background/95 px-3 backdrop-blur {editorRoute ? 'lg:hidden' : ''}">
			<Sheet.Root bind:open={mobileNavOpen}>
				<Sheet.Trigger>
					{#snippet child({ props })}
						<Button {...props} variant="ghost" size="icon" class="lg:hidden" aria-label="Open workspace navigation">
							<Menu aria-hidden="true" />
						</Button>
					{/snippet}
				</Sheet.Trigger>
				<Sheet.Content side="left" class="w-[min(19rem,86vw)] p-0" aria-label="Workspace navigation">
					<div class="flex h-11 items-center border-b border-border px-3">
						<a href="/app/workflows" class="flex items-center gap-2 rounded-md focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-ring">
							<span aria-hidden="true" class="grid size-5 place-items-center rounded bg-primary text-[0.625rem] font-bold tracking-tight text-primary-foreground">K</span>
							<span class="text-[0.8125rem] font-semibold tracking-tight">KilasFlow</span>
						</a>
					</div>
					<div class="px-2 py-2">
						<DashboardNav onNavigate={() => (mobileNavOpen = false)} />
					</div>
				</Sheet.Content>
			</Sheet.Root>

			<div class="min-w-0">
				<p class="truncate text-[0.8125rem] font-semibold tracking-tight">{sectionTitle}</p>
			</div>
		</header>

		<main id="dashboard-content" tabindex="-1" class:min-h-0={editorRoute} class={editorRoute ? 'h-[calc(100dvh-2.75rem)] overflow-hidden lg:h-dvh' : 'min-w-0 px-4 py-5 sm:px-5'}>
			{@render children()}
		</main>
	</section>
</div>
