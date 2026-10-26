<script lang="ts">
	import { cn } from '$lib/utils.js';
	import { statusIcon, statusLabel, statusTone } from '$lib/workflow-editor/execution';

	// One status pill for every surface that names a state (FEAT-r8ph93): the
	// execution lists and replay, the version panel's roles and the schedules'
	// active/paused chips. The tone comes from the single status→token map in
	// execution.ts, and the label never travels without a glyph — a dot for a
	// status the map does not know, so colour is never the only carrier.
	let {
		status,
		label,
		class: className
	}: {
		/** Any status the map knows, or an unknown one (rendered neutral with a dot). */
		status: string;
		/** Overrides the map's label, for roles whose wording lives elsewhere. */
		label?: string;
		class?: string;
	} = $props();

	const Icon = $derived(statusIcon(status));
	const spins = $derived(status === 'running' || status === 'cancelling');
</script>

<span
	class={cn(
		'inline-flex items-center gap-1 rounded-full border px-2 py-0 text-(--kf-text-2xs) font-medium',
		statusTone(status),
		className
	)}
>
	{#if Icon}
		<Icon aria-hidden="true" class="size-3 shrink-0 {spins ? 'animate-spin motion-reduce:animate-none' : ''}" />
	{:else}
		<span aria-hidden="true" class="size-1.5 shrink-0 rounded-full bg-current"></span>
	{/if}
	{label ?? statusLabel(status)}
</span>
