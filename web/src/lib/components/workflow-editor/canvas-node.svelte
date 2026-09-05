<script lang="ts">
	import { Handle, NodeToolbar, Position, useNodeConnections, type NodeProps } from '@xyflow/svelte';
	import Plus from '@lucide/svelte/icons/plus';
	import Trash2 from '@lucide/svelte/icons/trash-2';

	import type { EditorFlowNode } from '$lib/workflow-editor/document';
	import { getCanvasActions } from '$lib/workflow-editor/canvas-actions';
	import { TILE, attachmentPorts, glyphClass, mainPorts, nodeSubtitle, nodeVisual, portOffset } from '$lib/workflow-editor/node-visual';

	let { data, selected }: NodeProps<EditorFlowNode> = $props();

	const actions = getCanvasActions();
	// One subscription covers every output: each connection carries the handle it
	// left from, so the ports are filtered locally rather than one hook per port.
	const sourceConnections = useNodeConnections({ handleType: 'source' });

	const visual = $derived(nodeVisual(data.definition));
	const node = $derived(data.workflowNode);
	const subtitle = $derived(nodeSubtitle(node, data.definition));
	const invalid = $derived(Boolean(data.validationMessage));

	const mainInputs = $derived(mainPorts(data.definition.inputs));
	const mainOutputs = $derived(mainPorts(data.definition.outputs));
	const attachmentInputs = $derived(attachmentPorts(data.definition.inputs));
	const attachmentOutputs = $derived(attachmentPorts(data.definition.outputs));
	const connectedPorts = $derived(new Set(sourceConnections.current.map((connection) => connection.sourceHandle)));

	// Only a branching node needs its outputs named on the canvas. A single
	// `main` port is the obvious one, and labelling it would be noise.
	const showOutputLabels = $derived(mainOutputs.length > 1);
	const editable = $derived(actions ? !actions.readOnly() : false);

	// The accent's hue held exactly, at a muted lightness and chroma. Mixing
	// toward another colour interpolates hue in a polar space and pulls every
	// accent toward whatever it was mixed with — against `--border` that turned
	// amber and red green and violet and azure cyan, and it carried the accent
	// at ~68% rather than the stated share, because `--border` has an alpha.
	const border = $derived(
		invalid
			? 'var(--destructive)'
			: selected
				? 'var(--node-accent)'
				: 'oklch(from var(--node-accent) 0.42 0.045 h)'
	);
</script>

<div class="relative" style={`--node-accent: ${visual.accent}`}>
	{#if editable}
		<NodeToolbar position={Position.Top} offset={8}>
			<div class="nodrag flex items-center gap-0.5 rounded-lg border border-border bg-popover p-0.5 shadow-md">
				<button
					type="button"
					class="grid size-6 place-items-center rounded-md text-muted-foreground hover:bg-destructive/10 hover:text-destructive focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring"
					aria-label={`Delete ${node.name}`}
					onclick={() => actions?.remove(node.id)}
				>
					<Trash2 aria-hidden="true" class="size-3.5" />
				</button>
			</div>
		</NodeToolbar>
	{/if}

	<!-- The tile is the whole node as far as Svelte Flow is concerned: the name
	     below is positioned outside it so edges meet the icon, not the text. -->
	<div
		class="flex items-center justify-center border bg-card transition-[border-color,box-shadow] {TILE[visual.shape]}"
		style={`border-color: ${border}; box-shadow: ${
			selected ? '0 0 0 2px color-mix(in oklch, var(--node-accent) 35%, transparent)' : '0 1px 2px oklch(0 0 0 / 30%)'
		}`}
	>
		<visual.icon class={glyphClass(visual.shape)} style="color: var(--node-accent)" aria-hidden="true" />
		{#if visual.shape === 'hub'}
			<span class="truncate text-[0.8125rem] font-semibold leading-tight">{node.name}</span>
		{/if}
	</div>

	{#if invalid}
		<!-- The message itself reaches assistive technology through the node's
		     accessible name, which Svelte Flow owns; this is the visible cue. -->
		<span
			aria-hidden="true"
			class="absolute -bottom-1 -right-1 grid size-4 place-items-center rounded-full border-2 border-background bg-destructive text-[0.5rem] font-bold text-destructive-foreground"
			title={data.validationMessage}
		>
			!
		</span>
	{/if}

	<!-- Name and the one parameter worth reading at a glance. The hub carries its
	     name inside the tile, so it only needs the subtitle here. -->
	<div class="pointer-events-none absolute left-1/2 top-full w-40 -translate-x-1/2 pt-1.5 text-center">
		{#if visual.shape !== 'hub'}
			<p class="truncate text-[0.8125rem] font-semibold leading-tight">{node.name}</p>
		{/if}
		{#if subtitle}
			<p class="truncate pt-0.5 font-mono text-[0.6875rem] leading-tight text-muted-foreground">{subtitle}</p>
		{/if}
	</div>

	{#each mainInputs as port, index (port.Name)}
		<Handle
			type="target"
			id={port.Name}
			position={Position.Left}
			style={`top: ${portOffset(index, mainInputs.length)}`}
			aria-label={`${node.name} input ${port.Name}`}
		>
			<span class="kf-port"></span>
		</Handle>
	{/each}

	{#each mainOutputs as port, index (port.Name)}
		{@const top = portOffset(index, mainOutputs.length)}
		<Handle type="source" id={port.Name} position={Position.Right} style={`top: ${top}`} aria-label={`${node.name} output ${port.Name}`}>
			<span class="kf-port"></span>
		</Handle>
		{#if showOutputLabels}
			<span class="pointer-events-none absolute left-full ml-2.5 -translate-y-1/2 font-mono text-[0.625rem] text-muted-foreground" style={`top: ${top}`}>
				{port.Name}
			</span>
		{/if}
		{#if editable && !connectedPorts.has(port.Name)}
			<!-- The shortest path to the next step: one click adds it already wired
			     to this port, which is why the toolbar has no add button. -->
			<button
				type="button"
				data-add-step={node.id}
				class="nodrag absolute -translate-y-1/2 grid size-6 place-items-center rounded-md border border-dashed border-border bg-card text-muted-foreground transition-colors hover:border-[var(--node-accent)] hover:text-[var(--node-accent)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
				style={`top: ${top}; left: calc(100% + ${showOutputLabels ? '3.25rem' : '1.5rem'})`}
				aria-label={`Add a step after ${node.name}${showOutputLabels ? ` on ${port.Name}` : ''}`}
				onclick={() => actions?.addFrom(node.id, port.Name)}
			>
				<Plus aria-hidden="true" class="size-3" />
			</button>
		{/if}
	{/each}

	{#each attachmentInputs as port, index (port.Name)}
		{@const left = portOffset(index, attachmentInputs.length)}
		<Handle
			type="target"
			id={port.Name}
			position={Position.Bottom}
			style={`left: ${left}`}
			aria-label={`${node.name} ${port.Name} attachment`}
		>
			<span class="kf-port kf-port-attachment"></span>
		</Handle>
		<span class="pointer-events-none absolute top-full -translate-x-1/2 pt-2 font-mono text-[0.625rem] text-muted-foreground" style={`left: ${left}`}>
			{port.Name}
		</span>
	{/each}

	{#each attachmentOutputs as port, index (port.Name)}
		<Handle
			type="source"
			id={port.Name}
			position={Position.Top}
			style={`left: ${portOffset(index, attachmentOutputs.length)}`}
			aria-label={`${node.name} provides ${port.Name}`}
		>
			<span class="kf-port kf-port-attachment"></span>
		</Handle>
	{/each}
</div>
