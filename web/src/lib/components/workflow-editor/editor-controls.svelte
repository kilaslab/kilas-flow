<script lang="ts">
	import { tick } from 'svelte';

	import { ControlButton, Controls, useSvelteFlow } from '@xyflow/svelte';
	import WandSparkles from '@lucide/svelte/icons/wand-sparkles';

	import * as m from '$lib/paraglide/messages.js';

	/**
	 * Canvas chrome that lives *inside* SvelteFlow so tidy can fit the view
	 * afterwards. Parent owns the document mutation; this only triggers it and
	 * re-frames the viewport like n8n's magic-wand control.
	 */
	let {
		locked = false,
		onTidy
	}: {
		locked?: boolean;
		onTidy: () => void;
	} = $props();

	const flow = useSvelteFlow();

	async function tidyUp() {
		if (locked) return;
		onTidy();
		await tick();
		await flow.fitView({ padding: 0.15, maxZoom: 1 });
	}
</script>

<Controls showLock={false} fitViewOptions={{ padding: 0.15, maxZoom: 1 }}>
	{#snippet children()}
		{#if !locked}
			<ControlButton
				type="button"
				title={m.canvas_tidy()}
				aria-label={m.canvas_tidy()}
				data-testid="tidy-up"
				onclick={() => void tidyUp()}
			>
				<WandSparkles aria-hidden="true" class="size-3.5" />
			</ControlButton>
		{/if}
	{/snippet}
</Controls>
