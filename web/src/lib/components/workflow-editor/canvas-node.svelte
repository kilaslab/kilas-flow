<script lang="ts">
	import { Handle, NodeResizer, NodeToolbar, Position, useNodeConnections, type NodeProps } from '@xyflow/svelte';
	import Pencil from '@lucide/svelte/icons/pencil';
	import Plus from '@lucide/svelte/icons/plus';
	import Trash2 from '@lucide/svelte/icons/trash-2';

	import * as m from '$lib/paraglide/messages.js';
	import type { EditorFlowNode } from '$lib/workflow-editor/document';
	import { getCanvasActions } from '$lib/workflow-editor/canvas-actions';
	import {
		DIAGNOSTIC_SEVERITY_LABELS,
		diagnosticSummaryLabel,
		getImportDiagnostics,
		worstSeverity
	} from '$lib/workflow-editor/import-diagnostics';
	import {
		TILE,
		attachmentLabelRows,
		attachmentPorts,
		friendlyOriginalType,
		glyphClass,
		isAnnotation,
		mainPorts,
		nodeChromeBorder,
		nodeChromeShadow,
		nodeIconBadgeClass,
		nodeShape,
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

	const node = $derived(data.workflowNode);
	// An n8n import keeps a node it has no equivalent for as a visible
	// placeholder and stores the source identity in the parameters capsule
	// (`originalType`, `originalTypeVersion`). The capsule marks the tile so
	// the placeholder is identifiable without opening it.
	const capsuleType = $derived(
		typeof node.parameters?.originalType === 'string' && node.parameters.originalType !== ''
			? node.parameters.originalType
			: null
	);
	const isPlaceholder = $derived(node.type === 'kilasflow.unsupported');

	// Ports come from the node's own parameters, not from the definition alone:
	// a Switch has one output per rule and a Merge as many inputs as it was
	// told to take, and both change as the user configures the node.
	//
	// A placeholder draws only the ports its edges actually use. Its definition
	// is the widest member of the arity family the import registered, so the
	// full declaration is 24 AI diamonds plus main in and out — three times
	// wider than the node it stands in for, with every label printed over its
	// neighbours. A placeholder can never run, so an unconnected port has
	// nothing to offer; when nothing connects at all, the plain main in/out
	// stay so the tile can still be wired by hand.
	const connectedHandles = $derived(
		new Set([...sourceConnections.current.map((c) => c.sourceHandle), ...targetConnections.current.map((c) => c.targetHandle)])
	);
	const effectiveDefinition = $derived.by(() => {
		if (!isPlaceholder) return data.definition;
		const keep = (declared: typeof data.definition.inputs) => {
			const wired = (declared ?? []).filter((port) => connectedHandles.has(port.name));
			return wired.length > 0 ? wired : mainPorts(declared ?? []).slice(0, 1);
		};
		const inputs = keep(data.definition.inputs);
		const outputs = keep(data.definition.outputs);
		if (inputs === data.definition.inputs && outputs === data.definition.outputs) return data.definition;
		return { ...data.definition, inputs, outputs };
	});
	const visual = $derived(nodeVisual(effectiveDefinition));
	const subtitle = $derived(
		isPlaceholder
			? // The raw original type ("@n8n/n8n-nodes-langchain.toolSerpApi") is
			  // noise under the tile; the readable words are the useful line, and
			  // the full string stays in the Unsupported pill's tooltip.
			  friendlyOriginalType(capsuleType)
			: nodeSubtitle(node, data.definition)
	);
	const invalid = $derived(Boolean(data.validationMessage));
	const runStatus = $derived(data.runStatus ?? null);
	const annotation = $derived(isAnnotation(data.definition));
	const palette = $derived(stickyPalette(node.parameters?.color));
	const runs = $derived(markdownRuns(node.parameters?.content));

	const ports = $derived(resolvedPorts(node, effectiveDefinition));
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

	// Attachment labels fan across this many rows under the hub (null: too many
	// to label), and the fill button sits below the deepest row instead of
	// printing over the first one.
	const attachmentRows = $derived(attachmentLabelRows(attachmentInputs.length));

	const chrome = $derived({ selected: Boolean(selected), invalid, runStatus });
	const border = $derived(nodeChromeBorder(chrome));
	const shadow = $derived(nodeChromeShadow(chrome));

	// What the import could not carry for this node.
	//
	// Read from context rather than from props, the way the canvas actions are:
	// nodes are rendered by Svelte Flow, so there is no prop path to a tile, and
	// the report belongs to the revision the host is showing rather than to the
	// node's own shape. Undefined means the host reads no report — a replay, or
	// a workflow nobody imported — and then this node has nothing to say.
	const importReport = getImportDiagnostics();
	const importIssues = $derived(
		importReport ? importReport().issues.filter((issue) => issue.nodeId === id) : []
	);
	const importSeverity = $derived(worstSeverity(importIssues));
	// Blocking is the only severity that decides whether the workflow runs, so
	// the badge carries it as an alarm; lossy and dropped are notes, not faults.
	const importBadgeClass = $derived(
		importSeverity === 'blocking'
			? 'bg-destructive text-destructive-foreground'
			: importSeverity === 'lossy'
				? 'bg-warning text-warning-foreground'
				: 'bg-muted-foreground text-background'
	);
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
		<p class="whitespace-pre-wrap break-words text-xs leading-4" style="color: var(--kf-sticky-text)">
			{#each runs as run, index (index)}
				{#if run.link}
					<!-- An http(s)-only target (the tokenizer keeps anything else as
					     text), opened detached so a note cannot navigate the editor. -->
					<a href={run.link} target="_blank" rel="noopener noreferrer nofollow" class="underline underline-offset-2" onclick={(event) => event.stopPropagation()}>{run.text}</a>
				{:else if run.image}
					<span class="mx-0.5 inline-flex max-w-full items-center gap-1 truncate rounded border border-current/30 bg-current/10 px-1 font-mono text-[0.625rem]" title={run.text}>🖼 {run.text}</span>
				{:else if run.bullet}
					<span class={run.code ? 'font-mono' : run.bold ? 'font-semibold' : ''}><span class="opacity-60">• </span>{run.text}</span>
				{:else}
					<span class={run.heading ? 'font-semibold' : run.code ? 'font-mono' : run.bold ? 'font-semibold' : ''}>{run.text}</span>
				{/if}
			{/each}
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
						aria-label={m.canvas_node_rename_aria({ name: node.name })}
						onclick={() => actions?.rename(node.id)}
					>
						<Pencil aria-hidden="true" class="size-3.5" />
					</button>
					<button
						type="button"
						class="grid size-6 place-items-center rounded-md text-muted-foreground hover:bg-destructive/10 hover:text-destructive focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring"
						aria-label={m.canvas_node_delete_aria({ name: node.name })}
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

		{#if importSeverity && importIssues.length > 0}
			<!-- What the import could not carry for this node, which used to be
			     visible only in the dialog that closed after the import. The badge
			     is one dot, so the reasons live in the title and the accessible
			     name and the click reopens the report they came from. -->
			<button
				type="button"
				data-import-diagnostic={importSeverity}
				class="nodrag absolute -right-1 -top-1 grid h-4 min-w-4 place-items-center rounded-full border-2 border-background px-0.5 text-[0.5rem] font-bold {importBadgeClass}"
				title={importIssues.map((issue) => `${DIAGNOSTIC_SEVERITY_LABELS[issue.severity]}: ${issue.reason}`).join(' · ')}
				aria-label={m.canvas_node_import_badge_aria({ summary: diagnosticSummaryLabel(importIssues), name: node.name })}
				onclick={() => importReport?.().openReport()}
			>
				{importIssues.length}
			</button>
		{/if}

		<!-- Name and the one parameter worth reading at a glance. The hub carries its
		     name inside the tile, so it only needs the subtitle here. Compact band
		     (w-32) matching the 68px tile pitch so long chains fit. -->
		<div class="pointer-events-none absolute left-1/2 top-full w-32 -translate-x-1/2 pt-1 text-center">
			{#if visual.shape !== 'hub'}
				<p class="truncate text-xs font-semibold leading-tight">{node.name}</p>
			{/if}
			{#if capsuleType}
				<p class="mx-auto mt-0.5 w-fit truncate rounded-full border border-destructive/30 bg-destructive/10 px-1.5 py-px text-[0.625rem] font-medium leading-tight text-destructive" title={m.canvas_node_unsupported_note({ type: capsuleType })}>{m.canvas_node_unsupported()}</p>
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
				aria-label={m.canvas_node_input_aria({ name: node.name, port: portLabel(port) })}
			>
				<span class="kf-port"></span>
			</Handle>
		{/each}

		{#each mainOutputs as port, index (port.name)}
			{@const top = portOffset(index, mainOutputs.length)}
			<Handle type="source" id={port.name} position={Position.Right} style={`top: ${top}`} aria-label={m.canvas_node_output_aria({ name: node.name, port: portLabel(port) })}>
				<span class="kf-port"></span>
			</Handle>
			{#if showOutputLabels}
				<!-- Above the wire, not centred on it: the edge leaves the handle at
				     its own vertical centre, and a label sitting there reads as a
				     strikethrough ("t̶r̶u̶e̶"). -->
				<span class="pointer-events-none absolute left-full ml-2.5 max-w-20 -translate-y-full truncate whitespace-nowrap font-mono text-[0.625rem] text-muted-foreground" style={`top: calc(${top} - 0.375rem)`}>
					{portLabel(port)}
				</span>
			{/if}
			{#if editable && !connectedPorts.has(port.name)}
				<!-- The shortest path to the next step: one click adds it already wired
				     to this port, which is why the toolbar has no add button. On a
				     labelled node it drops below the wire line, where the outgoing
				     edge of a connected sibling used to run through it. -->
				<button
					type="button"
					data-add-step={node.id}
					class="nodrag absolute grid size-6 place-items-center rounded-md border border-dashed border-border bg-card text-muted-foreground transition-colors hover:border-[var(--node-accent)] hover:text-[var(--node-accent)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
					style={`top: ${showOutputLabels ? `calc(${top} + 0.375rem)` : top}; left: calc(100% + ${showOutputLabels ? '3.25rem' : '1.5rem'}); ${showOutputLabels ? '' : 'transform: translateY(-50%);'}`}
					aria-label={showOutputLabels ? m.canvas_node_add_after_port_aria({ name: node.name, port: portLabel(port) }) : m.canvas_node_add_after_aria({ name: node.name })}
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
				aria-label={
					port.required && empty
						? m.canvas_node_attachment_required_aria({ name: node.name, port: portLabel(port) })
						: m.canvas_node_attachment_aria({ name: node.name, port: portLabel(port) })
				}
			>
				<span class="kf-port kf-port-attachment"></span>
			</Handle>
			{#if editable && empty}
				<!-- Each agent slot fills itself, filtered to what can attach there:
				     previously the only way was the generic picker plus a manual drag
				     onto a 10px handle. It sits below the label rows, which used to
				     print the first row over the button. -->
				<button
					type="button"
					data-add-attachment={node.id}
					class="nodrag absolute -translate-x-1/2 grid size-5 place-items-center rounded-full border border-dashed border-border bg-card text-muted-foreground transition-colors hover:border-[var(--node-accent)] hover:text-[var(--node-accent)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
					style={`left: ${left}; top: calc(100% + ${attachmentRows === null ? '1.25' : `${0.375 + attachmentRows * 1.25}`}rem)`}
					aria-label={m.canvas_node_attachment_add_aria({ name: node.name, port: portLabel(port) })}
					onclick={() => actions?.addAttached(node.id, port.name, port.kind)}
				>
					<Plus aria-hidden="true" class="size-2.5" />
				</button>
			{/if}
			<!-- Labels fan across as many rows as the port spacing needs, one row
			     per index modulo the count: four of them across a 144px tile used to
			     collide into one unreadable line ("Chat Mode|Tools"). -->
			{#if attachmentRows !== null}
				{@const row = index % attachmentRows}
				<span
					class="pointer-events-none absolute -translate-x-1/2 max-w-12 truncate font-mono text-[0.625rem] leading-tight text-muted-foreground"
					style={`left: ${left}; top: calc(100% + ${0.125 + row * 1.25}rem)`}
					title={port.required && empty ? m.canvas_node_port_required_title({ port: portLabel(port) }) : portLabel(port)}
				>
					{portLabel(port)}{#if port.required && empty}<span class="text-destructive" aria-hidden="true"> *</span>{/if}
				</span>
			{/if}
		{/each}

		{#each attachmentOutputs as port, index (port.name)}
			<Handle
				type="source"
				id={port.name}
				position={Position.Top}
				style={`left: ${portOffset(index, attachmentOutputs.length)}`}
				aria-label={m.canvas_node_attachment_provides_aria({ name: node.name, port: portLabel(port) })}
			>
				<span class="kf-port kf-port-attachment"></span>
			</Handle>
		{/each}
	</div>
{/if}
