<script lang="ts">
	import { Handle, NodeResizer, NodeToolbar, Position, useNodeConnections, type NodeProps } from '@xyflow/svelte';
	import Pencil from '@lucide/svelte/icons/pencil';
	import Plus from '@lucide/svelte/icons/plus';
	import Trash2 from '@lucide/svelte/icons/trash-2';

	import type { EditorFlowNode } from '$lib/workflow-editor/document';
	import { getCanvasActions } from '$lib/workflow-editor/canvas-actions';
	import {
		TILE,
		attachmentPorts,
		glyphClass,
		isAnnotation,
		mainPorts,
		nodeChromeBorder,
		nodeChromeShadow,
		nodeIconBadgeClass,
		nodeSubtitle,
		nodeVisual,
		portOffset
	} from '$lib/workflow-editor/node-visual';
	import { portLabel, resolvedPorts } from '$lib/workflow-editor/ports';
	import { markdownRuns, stickyPalette } from '$lib/workflow-editor/sticky';

	let { id, data, selected }: NodeProps<EditorFlowNode> = $props();

	const actions = getCanvasActions();
	// Two subscriptions, one per direction: each connection carries the handle it
	// left from, so the ports are filtered locally rather than one hook per port.
	const sourceConnections = useNodeConnections({ handleType: 'source' });
	const targetConnections = useNodeConnections({ handleType: 'target' });

	const visual = $derived(nodeVisual(data.definition));
	const node = $derived(data.workflowNode);
	const subtitle = $derived(nodeSubtitle(node, data.definition));
	const invalid = $derived(Boolean(data.validationMessage));
	const runStatus = $derived(data.runStatus ?? null);
	const annotation = $derived(isAnnotation(data.definition));
	const palette = $derived(stickyPalette(node.parameters?.color));
	const runs = $derived(markdownRuns(node.parameters?.content));
	// An n8n import keeps a node it has no equivalent for as a visible
	// placeholder and stores the source identity in the parameters capsule
	// (`originalType`, `originalTypeVersion`). The capsule marks the tile so
	// the placeholder is identifiable without opening it.
	const capsuleType = $derived(
		typeof node.parameters?.originalType === 'string' && node.parameters.originalType !== ''
			? node.parameters.originalType
			: null
	);

	// Ports come from the node's own parameters, not from the definition alone:
	// a Switch has one output per rule and a Merge as many inputs as it was
	// told to take, and both change as the user configures the node.
	const ports = $derived(resolvedPorts(node, data.definition));
	const mainInputs = $derived(mainPorts(ports.inputs));
	const mainOutputs = $derived(mainPorts(ports.outputs));
	const attachmentInputs = $derived(attachmentPorts(ports.inputs));
	const attachmentOutputs = $derived(attachmentPorts(ports.outputs));
	const connectedPorts = $derived(new Set(sourceConnections.current.map((connection) => connection.sourceHandle)));
	const filledPorts = $derived(new Set(targetConnections.current.map((connection) => connection.targetHandle)));

	// Only a branching node needs its outputs named on the canvas. A single
	// `main` port is the obvious one, and labelling it would be noise.
	const showOutputLabels = $derived(mainOutputs.length > 1);
	const editable = $derived(actions ? !actions.readOnly() : false);

	const chrome = $derived({ selected: Boolean(selected), invalid, runStatus });
	const border = $derived(nodeChromeBorder(chrome));
	const shadow = $derived(nodeChromeShadow(chrome));
</script>

{#if annotation}
	<!-- A sticky note is the annotation itself: a coloured, sized rectangle
	     behind the graph, rendered as text rather than as a tile. Its content
	     arrives from an imported file, so it is tokenised into runs of text and
	     never interpreted as markup. -->
	<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
	<div
		class="relative flex h-full w-full flex-col overflow-auto rounded-md border p-2 text-left"
		style={`background: ${palette.fill}; border-color: ${palette.border}`}
		data-testid="sticky-note"
		role="note"
		ondblclick={() => actions?.rename(node.id)}
	>
		<p class="whitespace-pre-wrap break-words text-xs leading-4 text-neutral-800">
			{#each runs as run}<span class={run.heading ? 'font-semibold' : run.code ? 'font-mono' : run.bold ? 'font-semibold' : ''}>{run.text}</span>{/each}
		</p>
		{#if editable && selected}
			<NodeResizer
				isVisible
				color={palette.border}
				minWidth={120}
				minHeight={80}
				onResizeEnd={(_event, { width, height }) => actions?.resize(node.id, Math.round(width), Math.round(height))}
			/>
		{/if}
	</div>
{:else}
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		class="relative"
		style={`--node-accent: ${visual.accent}`}
		ondblclick={() => actions?.rename(node.id)}
		data-selected={selected ? 'true' : undefined}
		data-invalid={invalid ? 'true' : undefined}
		data-run-status={runStatus ?? undefined}
	>
		{#if editable}
			<NodeToolbar position={Position.Top} offset={8}>
				<div class="nodrag flex items-center gap-0.5 rounded-lg border border-border bg-popover p-0.5 shadow-md">
					<button
						type="button"
						class="grid size-6 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring"
						aria-label={`Rename ${node.name}`}
						onclick={() => actions?.rename(node.id)}
					>
						<Pencil aria-hidden="true" class="size-3.5" />
					</button>
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

		<!-- Card-like tile: accent icon badge on a raised surface so chrome reads at low zoom. -->
		<div
			class="kf-node-tile flex items-center justify-center gap-2.5 border bg-card transition-[border-color,box-shadow] {TILE[visual.shape]}"
			style={`border-color: ${border}; box-shadow: ${shadow}`}
		>
			<span
				class={nodeIconBadgeClass(visual.shape)}
				style="border-color: color-mix(in oklch, var(--node-accent) 32%, transparent); background: color-mix(in oklch, var(--node-accent) 16%, transparent)"
			>
				{#if visual.iconURL}
					<img src={visual.iconURL} alt="" loading="lazy" decoding="async" class={glyphClass(visual.shape)} />
				{:else}
					<visual.icon class={glyphClass(visual.shape)} style="color: var(--node-accent)" aria-hidden="true" />
				{/if}
			</span>
			{#if visual.shape === 'hub'}
				<span class="truncate text-xs font-semibold leading-tight">{node.name}</span>
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
		     name inside the tile, so it only needs the subtitle here. Compact band
		     (w-32) matching the 68px tile pitch so long chains fit. -->
		<div class="pointer-events-none absolute left-1/2 top-full w-32 -translate-x-1/2 pt-1 text-center">
			{#if visual.shape !== 'hub'}
				<p class="truncate text-xs font-semibold leading-tight">{node.name}</p>
			{/if}
			{#if capsuleType}
				<p class="mx-auto mt-0.5 w-fit truncate rounded-full border border-destructive/30 bg-destructive/10 px-1.5 py-px text-[0.625rem] font-medium leading-tight text-destructive" title={`Unsupported node imported from n8n as ${capsuleType}`}>Unsupported</p>
			{/if}
			{#if subtitle}
				<p class="truncate pt-0.5 font-mono text-[0.625rem] leading-tight text-muted-foreground">{subtitle}</p>
			{/if}
		</div>

		{#each mainInputs as port, index (port.name)}
			<Handle
				type="target"
				id={port.name}
				position={Position.Left}
				style={`top: ${portOffset(index, mainInputs.length)}`}
				aria-label={`${node.name} input ${portLabel(port)}`}
			>
				<span class="kf-port"></span>
			</Handle>
		{/each}

		{#each mainOutputs as port, index (port.name)}
			{@const top = portOffset(index, mainOutputs.length)}
			<Handle type="source" id={port.name} position={Position.Right} style={`top: ${top}`} aria-label={`${node.name} output ${portLabel(port)}`}>
				<span class="kf-port"></span>
			</Handle>
			{#if showOutputLabels}
				<span class="pointer-events-none absolute left-full ml-2.5 -translate-y-1/2 font-mono text-[0.625rem] text-muted-foreground" style={`top: ${top}`}>
					{portLabel(port)}
				</span>
			{/if}
			{#if editable && !connectedPorts.has(port.name)}
				<!-- The shortest path to the next step: one click adds it already wired
				     to this port, which is why the toolbar has no add button. -->
				<button
					type="button"
					data-add-step={node.id}
					class="nodrag absolute -translate-y-1/2 grid size-6 place-items-center rounded-md border border-dashed border-border bg-card text-muted-foreground transition-colors hover:border-[var(--node-accent)] hover:text-[var(--node-accent)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
					style={`top: ${top}; left: calc(100% + ${showOutputLabels ? '3.25rem' : '1.5rem'})`}
					aria-label={`Add a step after ${node.name}${showOutputLabels ? ` on ${portLabel(port)}` : ''}`}
					onclick={() => actions?.addFrom(node.id, port.name)}
				>
					<Plus aria-hidden="true" class="size-3" />
				</button>
			{/if}
		{/each}

		{#each attachmentInputs as port, index (port.name)}
			{@const left = portOffset(index, attachmentInputs.length)}
			{@const empty = !filledPorts.has(port.name)}
			<Handle
				type="target"
				id={port.name}
				position={Position.Bottom}
				style={`left: ${left}`}
				aria-label={`${node.name} ${portLabel(port)} attachment${port.required && empty ? ', required and empty' : ''}`}
			>
				<span class="kf-port kf-port-attachment"></span>
			</Handle>
			{#if editable && empty}
				<!-- Each agent slot fills itself, filtered to what can attach there:
				     previously the only way was the generic picker plus a manual drag
				     onto a 10px handle. -->
				<button
					type="button"
					data-add-attachment={node.id}
					class="nodrag absolute -translate-x-1/2 grid size-5 place-items-center rounded-full border border-dashed border-border bg-card text-muted-foreground transition-colors hover:border-[var(--node-accent)] hover:text-[var(--node-accent)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
					style={`left: ${left}; top: calc(100% + 1.25rem)`}
					aria-label={`Add a ${portLabel(port)} to ${node.name}`}
					onclick={() => actions?.addAttached(node.id, port.name, port.kind)}
				>
					<Plus aria-hidden="true" class="size-2.5" />
				</button>
			{/if}
			<!-- Labels sit under the hub two rows deep and truncate: four of them
			     across a 240px tile used to collide into one unreadable line. -->
			{@const row = index % 2}
			<span
				class="pointer-events-none absolute -translate-x-1/2 max-w-16 truncate font-mono text-[0.625rem] text-muted-foreground"
				style={`left: ${left}; top: calc(100% + ${row === 0 ? '0.125rem' : '2.5rem'})`}
				title={`${portLabel(port)}${port.required && empty ? ' (required)' : ''}`}
			>
				{portLabel(port)}{#if port.required && empty}<span class="text-destructive" aria-hidden="true"> *</span>{/if}
			</span>
		{/each}

		{#each attachmentOutputs as port, index (port.name)}
			<Handle
				type="source"
				id={port.name}
				position={Position.Top}
				style={`left: ${portOffset(index, attachmentOutputs.length)}`}
				aria-label={`${node.name} provides ${portLabel(port)}`}
			>
				<span class="kf-port kf-port-attachment"></span>
			</Handle>
		{/each}
	</div>
{/if}
