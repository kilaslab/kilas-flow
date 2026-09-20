<script lang="ts">
	import { BaseEdge, EdgeLabel, getBezierPath, getSmoothStepPath, type EdgeProps } from '@xyflow/svelte';
	import Plus from '@lucide/svelte/icons/plus';
	import Trash2 from '@lucide/svelte/icons/trash-2';

	import { getCanvasActions } from '$lib/workflow-editor/canvas-actions';
	import type { EditorFlowEdge } from '$lib/workflow-editor/document';

	/**
	 * A connection with the two actions n8n puts on one: insert a step in the
	 * middle of it, or delete it.
	 *
	 * Both were previously reachable only by selecting the wire and then
	 * finding the toolbar, which is a detour for the most common edit on a
	 * finished workflow — dropping a step into an existing chain.
	 */
	let {
		id,
		type,
		sourceX,
		sourceY,
		targetX,
		targetY,
		sourcePosition,
		targetPosition,
		markerEnd,
		style,
		selected,
		interactionWidth = 20
	}: EdgeProps<EditorFlowEdge> = $props();

	const actions = getCanvasActions();
	const editable = $derived(actions ? !actions.readOnly() : false);
	const geometry = $derived(
		type === 'default'
			? getBezierPath({ sourceX, sourceY, sourcePosition, targetX, targetY, targetPosition })
			: getSmoothStepPath({ sourceX, sourceY, sourcePosition, targetX, targetY, targetPosition })
	);

	// The buttons live in the edge-label layer, which is outside this edge's own
	// group. Moving the pointer from the wire to them crosses that boundary, so
	// leaving is delayed long enough for the trip — otherwise the buttons
	// vanish as the pointer arrives.
	let hovered = $state(false);
	let hideTimer: ReturnType<typeof setTimeout> | undefined;
	function enter() {
		clearTimeout(hideTimer);
		hovered = true;
	}
	function leave() {
		clearTimeout(hideTimer);
		hideTimer = setTimeout(() => (hovered = false), 200);
	}
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<g onmouseenter={enter} onmouseleave={leave} role="presentation">
	<BaseEdge {id} path={geometry[0]} {markerEnd} {style} {interactionWidth} />
	{#if editable && (hovered || selected)}
		<EdgeLabel x={geometry[1]} y={geometry[2]} class="nodrag nopan" onmouseenter={enter} onmouseleave={leave}>
			<div class="flex items-center gap-0.5 rounded-md border border-border bg-popover p-0.5 shadow-md">
				<button
					type="button"
					class="grid size-6 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-1"
					aria-label="Insert a step into this connection"
					onclick={() => actions?.splice(id)}
				>
					<Plus aria-hidden="true" class="size-3.5" />
				</button>
				<button
					type="button"
					class="grid size-6 place-items-center rounded-md text-muted-foreground hover:bg-destructive/10 hover:text-destructive focus-visible:outline-2 focus-visible:outline-offset-1"
					aria-label="Delete this connection"
					onclick={() => actions?.removeEdge(id)}
				>
					<Trash2 aria-hidden="true" class="size-3.5" />
				</button>
			</div>
		</EdgeLabel>
	{/if}
</g>
