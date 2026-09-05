<script lang="ts">
	import { page } from '$app/state';
	import Menu from '@lucide/svelte/icons/menu';

	import DashboardNav from '$lib/components/dashboard/dashboard-nav.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Sheet from '$lib/components/ui/sheet';

	let { children } = $props();
	let mobileNavOpen = $state(false);

	const sectionTitle = $derived.by(() => {
		if (page.url.pathname.startsWith('/executions')) return 'Executions';
		if (page.url.pathname.startsWith('/schedules')) return 'Schedules';
		if (page.url.pathname.startsWith('/credentials')) return 'Credentials';
		if (page.url.pathname.startsWith('/settings')) return 'Settings';
		return 'Workflows';
	});
	const editorRoute = $derived(/^\/app\/workflows\/[^/]+$/.test(page.url.pathname));
</script>

<svelte:head>
	<meta name="theme-color" content="#111a18" />
</svelte:head>

<div data-dashboard-shell class="min-h-dvh bg-background lg:grid lg:grid-cols-[15.5rem_minmax(0,1fr)]">
	<aside class="hidden border-r border-sidebar-border bg-sidebar lg:sticky lg:top-0 lg:flex lg:h-dvh lg:flex-col">
		<div class="flex h-16 items-center border-b border-sidebar-border px-5">
			<a href="/app/workflows" class="flex items-center gap-2.5 rounded-lg focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-sidebar-ring">
				<span aria-hidden="true" class="grid size-7 place-items-center rounded-md bg-sidebar-primary text-xs font-bold tracking-tight text-sidebar-primary-foreground">K</span>
				<span class="text-sm font-semibold tracking-tight text-sidebar-foreground">KilasFlow</span>
			</a>
		</div>
		<div class="flex-1 px-3 py-5">
			<DashboardNav />
		</div>
		<div class="border-t border-sidebar-border px-5 py-4 text-xs leading-5 text-muted-foreground">
			Build workflows your product can own.
		</div>
	</aside>

	<section class="min-w-0">
		<header class="sticky top-0 z-20 flex h-16 items-center gap-3 border-b border-border bg-background/95 px-4 backdrop-blur sm:px-6 lg:px-8">
			<Sheet.Root bind:open={mobileNavOpen}>
				<Sheet.Trigger>
					{#snippet child({ props })}
						<Button {...props} variant="ghost" size="icon" class="lg:hidden" aria-label="Open workspace navigation">
							<Menu aria-hidden="true" />
						</Button>
					{/snippet}
				</Sheet.Trigger>
				<Sheet.Content side="left" class="w-[min(19rem,86vw)] p-0" aria-label="Workspace navigation">
					<div class="flex h-16 items-center border-b border-border px-5">
						<a href="/app/workflows" class="flex items-center gap-2.5 rounded-lg focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-ring">
							<span aria-hidden="true" class="grid size-7 place-items-center rounded-md bg-primary text-xs font-bold tracking-tight text-primary-foreground">K</span>
							<span class="text-sm font-semibold tracking-tight">KilasFlow</span>
						</a>
					</div>
					<div class="px-3 py-5">
						<DashboardNav onNavigate={() => (mobileNavOpen = false)} />
					</div>
				</Sheet.Content>
			</Sheet.Root>

			<div class="min-w-0">
				<p class="truncate text-sm font-semibold tracking-tight">{sectionTitle}</p>
			</div>
			<div class="ml-auto hidden text-xs text-muted-foreground sm:block">Internal workspace</div>
		</header>

		<main id="dashboard-content" tabindex="-1" class:min-h-0={editorRoute} class={editorRoute ? 'h-[calc(100dvh-4rem)] overflow-hidden' : 'min-w-0 px-4 py-7 sm:px-6 sm:py-9 lg:px-8'}>
			{@render children()}
		</main>
	</section>
</div>
