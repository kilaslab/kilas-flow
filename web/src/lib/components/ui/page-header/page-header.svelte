<script lang="ts">
	import type { Snippet } from 'svelte';

	// One page-header pattern for every dashboard page (FEAT-npc3ge): the H1
	// lives in the content — the top bar carries only the breadcrumb — with an
	// optional description under it and the page's one action to the right.
	// What each page says is its own business; the shape is shared, so five
	// lists can no longer grow five headings.
	let {
		title,
		description,
		actions
	}: {
		title: string;
		/** One muted line under the title; carries its own <p>. */
		description?: Snippet;
		/** The page's primary action (a dialog trigger, a refresh button). */
		actions?: Snippet;
	} = $props();
</script>

<header class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
	<div class="min-w-0 max-w-xl">
		<h1 class="text-base font-semibold tracking-tight">{title}</h1>
		{@render description?.()}
	</div>
	{#if actions}
		<div class="flex w-full shrink-0 items-center gap-2 sm:w-auto">
			{@render actions()}
		</div>
	{/if}
</header>
