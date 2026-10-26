<script lang="ts">
	import * as m from '$lib/paraglide/messages.js';
	import { nudgeRailWidth, startRailResize } from '$lib/workflow-editor/rail-width.svelte';

	// The right rail's resize grip: pointer-dragged, arrow-key nudged. The
	// WAI-ARIA window-splitter pattern — role=separator, focusable, arrows —
	// which is why the two a11y ignores below are earned rather than silenced:
	// a separator is non-interactive only until it is focusable and valued.
	let { label = m.editor_resize_rail_aria() }: { label?: string } = $props();
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<div
	role="separator"
	aria-orientation="vertical"
	aria-label={label}
	tabindex="0"
	title={label}
	class="absolute inset-y-0 left-0 z-10 w-2 -translate-x-1/2 cursor-col-resize touch-none transition-colors hover:bg-(--kf-selection)/40 focus-visible:outline-2 focus-visible:-outline-offset-2"
	onpointerdown={startRailResize}
	onkeydown={(event) => {
		if (event.key === 'ArrowLeft') {
			event.preventDefault();
			nudgeRailWidth(16);
		}
		if (event.key === 'ArrowRight') {
			event.preventDefault();
			nudgeRailWidth(-16);
		}
	}}
></div>
