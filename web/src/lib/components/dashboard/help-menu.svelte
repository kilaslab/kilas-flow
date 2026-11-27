<script lang="ts">
	import CircleHelp from '@lucide/svelte/icons/circle-help';
	import ExternalLink from '@lucide/svelte/icons/external-link';

	import { createGetHealth } from '$lib/api/generated/system/system';
	import type { HealthOutputBody } from '$lib/api/generated/models';
	import * as Dropdown from '$lib/components/ui/dropdown-menu';
	import { helpLinks } from '$lib/dashboard/help-links';
	import * as m from '$lib/paraglide/messages.js';

	/**
	 * The Help menu (FEAT-p01rcw): the API reference, the OpenAPI document, the
	 * changelog and the running version, behind one compact trigger.
	 *
	 * It sits in the sidebar footer beside the language switcher, for the same
	 * reason that control does: the dashboard header is `lg:hidden` on the
	 * editor route, and help is most needed on that screen. The mobile sheet
	 * renders the same component.
	 *
	 * The version is read from the health probe the Settings page already uses,
	 * so the menu names the build the server is running, not the one the web app
	 * was compiled with. Keyboard shortcuts are not listed: that overlay is owned
	 * by the workflow editor (press `?` there) and has no entry point outside it.
	 */
	let { collapsed = false }: { collapsed?: boolean } = $props();

	const health = createGetHealth<HealthOutputBody>(() => ({
		query: {
			select: (response) => {
				if (response.status !== 200) throw new Error(m.settings_error_unexpected_health());
				return response.data;
			}
		}
	}));

	const version = $derived(
		health.data ? m.nav_help_version({ version: health.data.version }) : health.isError ? m.nav_help_version_unavailable() : null
	);
</script>

<Dropdown.Root>
	<Dropdown.Trigger
		aria-label={collapsed ? m.nav_help() : undefined}
		title={collapsed ? m.nav_help() : undefined}
		class="flex h-7 w-full items-center rounded-md text-[0.8125rem] font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-sidebar-accent-foreground active:bg-sidebar-accent focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring {collapsed
			? 'justify-center px-0'
			: 'gap-3 px-2'}"
	>
		<CircleHelp aria-hidden="true" class={collapsed ? 'size-4' : 'size-3.5'} />
		{#if !collapsed}
			{m.nav_help()}
		{/if}
	</Dropdown.Trigger>
	<Dropdown.Content side="top" align="start" class="w-52">
		{#each helpLinks as link (link.key)}
			<Dropdown.Item>
				{#snippet child({ props })}
					{#if link.newTab}
						<a {...props} href={link.href} target="_blank" rel="noreferrer">
							{link.label()}
							<ExternalLink aria-hidden="true" class="ml-auto size-3.5 text-muted-foreground" />
							<span class="sr-only">{m.nav_help_opens_new_tab()}</span>
						</a>
					{:else}
						<!-- Served by the Go binary, not a SvelteKit route: rel="external" makes the router load the page instead of resolving it client-side. -->
						<a {...props} href={link.href} rel="external">{link.label()}</a>
					{/if}
				{/snippet}
			</Dropdown.Item>
		{/each}
		{#if version}
			<Dropdown.Separator />
			<Dropdown.Label class="font-mono">{version}</Dropdown.Label>
		{/if}
	</Dropdown.Content>
</Dropdown.Root>
