<script lang="ts">
	import type { Definition } from '$lib/api/generated/models';
	import { nodeVisual } from '$lib/workflow-editor/node-visual';

	/**
	 * A node's icon at whatever size the surface needs. The canvas, the picker,
	 * and the inspector all render this, so a node looks the same everywhere it
	 * appears and the mapping lives in exactly one place.
	 */
	let {
		definition,
		size = 'md',
		muted = false,
		label
	}: {
		definition: Definition;
		size?: 'sm' | 'md' | 'lg';
		muted?: boolean;
		/** Give the icon an accessible name where it is the only cue for something. */
		label?: string;
	} = $props();

	const visual = $derived(nodeVisual(definition));
	const box = { sm: 'size-6 rounded-md', md: 'size-7 rounded-lg', lg: 'size-9 rounded-xl' };
	const glyph = { sm: 'size-3.5', md: 'size-4', lg: 'size-[1.125rem]' };
</script>

<span
	role={label ? 'img' : undefined}
	aria-label={label}
	aria-hidden={label ? undefined : 'true'}
	class="grid shrink-0 place-items-center border {box[size]}"
	style={`--node-accent: ${visual.accent}; border-color: color-mix(in oklch, var(--node-accent) 28%, transparent); background: color-mix(in oklch, var(--node-accent) ${muted ? 8 : 14}%, transparent)`}
>
	{#if visual.iconURL}
		<!-- A node that ships its own artwork. Rendered through <img> and never
		     through {@html}: SVG is an active document format, this editor is
		     embedded in customer pages, and an <img> gives the browser's own
		     image sandbox for free. -->
		<img src={visual.iconURL} alt="" loading="lazy" decoding="async" class={glyph[size]} />
	{:else}
		<visual.icon class={glyph[size]} style="color: var(--node-accent)" />
	{/if}
</span>
