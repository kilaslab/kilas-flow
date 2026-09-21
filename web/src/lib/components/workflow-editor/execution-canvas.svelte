<script lang="ts">

	import { Background, BackgroundVariant, Controls, SvelteFlow } from '@xyflow/svelte';

	import type { Definition, Document, ExecutionNodeRunResource } from '$lib/api/generated/models';
	import * as m from '$lib/paraglide/messages.js';
	import { documentFromCanvas, type EditorFlowEdge, type EditorFlowNode } from '$lib/workflow-editor/document';
	import { edgeItemCounts, nodeRunStatus, statusLabel } from '$lib/workflow-editor/execution';

	import ExecutionCanvasNode from './execution-canvas-node.svelte';

	let {
		document,
		definitions,
		runs,
		statuses,
		selectedNodeID = $bindable(null)
	}: {
		document: Document;
		definitions: Definition[];
		runs: Map<string, ExecutionNodeRunResource>;
		// Node statuses folded from the durable trace plus any live events, so
		// the canvas has one source rather than two that can disagree.
		statuses?: Map<string, string>;
		selectedNodeID?: string | null;
	} = $props();

	const nodeTypes = { workflow: ExecutionCanvasNode };
	const counts = $derived(edgeItemCounts(document.connections, document.nodes, definitions, runs));
	const projection = $derived(documentFromCanvas(document, definitions));

	// Svelte Flow needs to own these arrays, but the execution view never writes
	// back: every mutation affordance below is disabled, so the bound arrays only
	// ever carry selection state.
	let nodes = $state.raw<EditorFlowNode[]>([]);
	let edges = $state.raw<EditorFlowEdge[]>([]);

	$effect(() => {
		nodes = projection.nodes.map((node) => {
			const runStatus = statuses?.get(node.id) ?? nodeRunStatus(node.id, runs);
			return {
				...node,
				draggable: false,
				connectable: false,
				deletable: false,
				selected: node.id === selectedNodeID,
				// Status is the reason this view exists, so it goes in the name the
				// node announces rather than only in a badge and a `title`.
				ariaLabel: `${node.ariaLabel}: ${statusLabel(runStatus)}`,
				data: { ...node.data, runStatus }
			};
		});
		edges = projection.edges.map((edge) => {
			const count = counts.get(edge.id);
			// A source that recorded no output has nothing to count, and that is
			// not the same as an edge that carried zero items: only the first
			// gets no label at all. The count itself is the catalog's one item
			// message, the same one the dashboard counts with.
			const items = count === undefined ? null : m.common_item_count({ count });
			return {
				...edge,
				deletable: false,
				selectable: false,
				animated: false,
				...(items === null ? {} : { label: items }),
				// The name the edge announces carries the same count as the label
				// it draws, from the one message, so the two cannot drift.
				ariaLabel: `${edge.ariaLabel}${items === null ? '' : `, ${items}`}`
			};
		});
	});

	function onSelectionChange({ nodes: selected }: { nodes: EditorFlowNode[] }) {
		selectedNodeID = selected[0]?.id ?? null;
	}

	/**
	 * SvelteFlow's own control chrome, in the runtime locale.
	 *
	 * The zoom buttons carry these as both their accessible name and their
	 * tooltip, so they are copy a sighted user reads — the library ships them in
	 * English and reads the override out of this prop. Derived rather than
	 * declared, so flipping the locale re-labels the buttons without a remount.
	 */
	const ariaLabels = $derived({
		'controls.ariaLabel': m.canvas_controls_aria(),
		'controls.zoomIn.ariaLabel': m.canvas_zoom_in(),
		'controls.zoomOut.ariaLabel': m.canvas_zoom_out(),
		'controls.fitView.ariaLabel': m.canvas_fit_view()
	});
</script>

<div class="relative size-full" data-testid="execution-canvas">
	<SvelteFlow
		bind:nodes
		bind:edges
		{nodeTypes}
		fitView
		fitViewOptions={{ padding: 0.25, maxZoom: 1 }}
		minZoom={0.3}
		nodesDraggable={false}
		nodesConnectable={false}
		elementsSelectable
		deleteKey={null}
		onselectionchange={onSelectionChange}
		ariaLabelConfig={ariaLabels}
	>
		<Background variant={BackgroundVariant.Dots} gap={16} size={1} patternColor="var(--border)" />
		<Controls showLock={false} />
	</SvelteFlow>

	<p class="pointer-events-none absolute left-2 top-2 z-10 rounded-md border border-border bg-card/90 px-1.5 py-0.5 text-[0.625rem] font-medium text-muted-foreground backdrop-blur">
		{m.executions_read_only_replay()}
	</p>
</div>
