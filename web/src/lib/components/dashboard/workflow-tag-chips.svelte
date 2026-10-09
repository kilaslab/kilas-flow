<script lang="ts">
	import * as m from '$lib/paraglide/messages.js';

	/**
	 * A workflow row's tags as chips: the first few, then how many more.
	 *
	 * Display-only spans on purpose. The row is a link, and a button or a link
	 * inside a link is invalid, so a chip is not a filter control: the tag
	 * filter sits above the list.
	 */
	let { tags, limit = 3 }: { tags: readonly string[]; limit?: number } = $props();

	const shown = $derived(tags.slice(0, limit));
	const hidden = $derived(tags.slice(limit));
</script>

{#if tags.length > 0}
	<span class="hidden min-w-0 shrink items-center gap-1 sm:flex">
		<span class="sr-only">{m.workflows_tags_label()}{' '}</span>
		{#each shown as tag (tag)}
			<span class="max-w-24 truncate rounded border border-border bg-muted/40 px-1.5 text-[0.6875rem] text-muted-foreground" title={tag}>{tag}</span>
		{/each}
		{#if hidden.length > 0}
			<span class="shrink-0 rounded border border-border bg-muted/40 px-1.5 text-[0.6875rem] text-muted-foreground" title={hidden.join(', ')}>{m.workflows_tags_more({ count: hidden.length })}</span>
		{/if}
	</span>
{/if}
