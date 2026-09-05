<script lang="ts">
	import '@xyflow/svelte/dist/style.css';

	import { Background, BackgroundVariant, Controls, SvelteFlow } from '@xyflow/svelte';

	import type { Definition, Document, ExecutionNodeRunResource } from '$lib/api/generated/models';
	import { documentFromCanvas, type EditorFlowEdge, type EditorFlowNode } from '$lib/workflow-editor/document';
	import { edgeItemCounts, nodeRunStatus } from '$lib/workflow-editor/execution';

	import ExecutionCanvasNode from './execution-canvas-node.svelte';

	let {
		document,
		definitions,
		runs,
		selectedNodeID = $bindable(null)
	}: {
		document: Document;
		definitions: Definition[];
		runs: Map<string, ExecutionNodeRunResource>;
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
		nodes = projection.nodes.map((node) => ({
			...node,
			draggable: false,
			connectable: false,
			deletable: false,
			selected: node.id === selectedNodeID,
			data: { ...node.data, runStatus: nodeRunStatus(node.id, runs) }
		}));
		edges = projection.edges.map((edge) => {
			const count = counts.get(edge.id);
			return {
				...edge,
				deletable: false,
				selectable: false,
				animated: false,
				...(count === undefined ? {} : { label: count === 1 ? '1 item' : `${count} items` }),
				ariaLabel: `${edge.ariaLabel}${count === undefined ? '' : `, ${count} items`}`
			};
		});
	});

	function onSelectionChange({ nodes: selected }: { nodes: EditorFlowNode[] }) {
		selectedNodeID = selected[0]?.id ?? null;
	}
</script>

<div class="relative size-full" data-testid="execution-canvas">
	<SvelteFlow
		bind:nodes
		bind:edges
		{nodeTypes}
		fitView
		nodesDraggable={false}
		nodesConnectable={false}
		elementsSelectable
		deleteKey={null}
		onselectionchange={onSelectionChange}
	>
		<Background variant={BackgroundVariant.Dots} gap={20} size={1} patternColor="var(--border)" />
		<Controls showLock={false} />
	</SvelteFlow>

	<p class="pointer-events-none absolute left-3 top-3 z-10 rounded-full border border-border bg-card/90 px-2.5 py-1 text-xs font-medium text-muted-foreground backdrop-blur">
		Read-only replay
	</p>
</div>
