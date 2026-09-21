<script lang="ts">
	import type { Component } from 'svelte';
	import { Handle, Position, type NodeProps } from '@xyflow/svelte';
	import Ban from '@lucide/svelte/icons/ban';
	import Check from '@lucide/svelte/icons/check';
	import CircleDashed from '@lucide/svelte/icons/circle-dashed';
	import LoaderCircle from '@lucide/svelte/icons/loader-circle';
	import Minus from '@lucide/svelte/icons/minus';
	import X from '@lucide/svelte/icons/x';

	import * as m from '$lib/paraglide/messages.js';
	import type { EditorFlowNode } from '$lib/workflow-editor/document';
	import { statusAccent, statusLabel } from '$lib/workflow-editor/execution';
	import {
		TILE,
		attachmentPorts,
		glyphClass,
		mainPorts,
		nodeChromeShadow,
		nodeIconBadgeClass,
		nodeSubtitle,
		nodeVisual,
		portOffset
	} from '$lib/workflow-editor/node-visual';

	/**
	 * The replay node keeps the editor node's geometry and ports so a graph reads
	 * the same in both views — both import the same tile table, so that holds by
	 * construction. What changes is what the border means: here it carries the
	 * recorded run status rather than the node's category, because that is the
	 * one thing a reader came to this view for.
	 */
	let { data, selected }: NodeProps<EditorFlowNode> = $props();

	const visual = $derived(nodeVisual(data.definition));
	const node = $derived(data.workflowNode);
	const subtitle = $derived(nodeSubtitle(node, data.definition));
	const status = $derived(data.runStatus ?? 'skipped');
	const accent = $derived(statusAccent(status));
	const reached = $derived(status !== 'skipped');

	const mainInputs = $derived(mainPorts(data.definition.inputs));
	const mainOutputs = $derived(mainPorts(data.definition.outputs));
	const attachmentInputs = $derived(attachmentPorts(data.definition.inputs));
	const attachmentOutputs = $derived(attachmentPorts(data.definition.outputs));
	const showOutputLabels = $derived(mainOutputs.length > 1);

	// A distinct glyph per status: hue alone cannot separate failed from
	// cancelled for a reader who does not see colour, and `title` is hover-only.
	const BADGE: Record<string, Component> = {
		succeeded: Check,
		failed: X,
		running: LoaderCircle,
		cancelled: Ban,
		cancelling: CircleDashed
	};
	const badge = $derived(BADGE[status] ?? Minus);

	const shadow = $derived(
		nodeChromeShadow({
			selected: Boolean(selected),
			invalid: status === 'failed',
			runStatus: reached ? status : null
		})
	);
</script>

<div class="relative" style={`--node-accent: ${accent}`} data-run-status={status} data-selected={selected ? 'true' : undefined}>
	<div
		class="kf-node-tile flex items-center justify-center gap-2.5 border bg-card {TILE[visual.shape]}"
		class:opacity-60={!reached}
		style={`border-color: ${reached ? 'var(--node-accent)' : 'var(--border)'}; box-shadow: ${
			reached || selected ? shadow : '0 1px 3px oklch(0 0 0 / 30%)'
		}`}
	>
		<span
			class={nodeIconBadgeClass(visual.shape)}
			style="border-color: color-mix(in oklch, var(--node-accent) 32%, transparent); background: color-mix(in oklch, var(--node-accent) {reached ? 16 : 8}%, transparent)"
		>
			<visual.icon
				class={glyphClass(visual.shape)}
				style={`color: ${reached ? 'var(--node-accent)' : 'var(--muted-foreground)'}`}
				aria-hidden="true"
			/>
		</span>
		{#if visual.shape === 'hub'}
			<span class="truncate text-xs font-semibold leading-tight">{node.name}</span>
		{/if}
	</div>

	{#if reached}
		{@const Badge = badge}
		<span
			aria-hidden="true"
			class="absolute -bottom-1 -right-1 grid size-4.5 place-items-center rounded-full border-2 border-background"
			style="background: var(--node-accent)"
			title={statusLabel(status)}
		>
			<Badge class="size-2.5 text-background {status === 'running' ? 'animate-spin' : ''}" />
		</span>
	{/if}

	<div class="pointer-events-none absolute left-1/2 top-full w-32 -translate-x-1/2 pt-1 text-center">
		{#if visual.shape !== 'hub'}
			<p class="truncate text-xs font-semibold leading-tight" class:text-muted-foreground={!reached}>{node.name}</p>
		{/if}
		{#if subtitle}
			<p class="truncate pt-0.5 font-mono text-[0.625rem] leading-tight text-muted-foreground">{subtitle}</p>
		{/if}
	</div>

	{#each mainInputs as port, index (port.name)}
		<Handle type="target" id={port.name} position={Position.Left} isConnectable={false} style={`top: ${portOffset(index, mainInputs.length)}`} aria-label={m.executions_main_input_aria({ name: node.name, port: port.name })}>
			<span class="kf-port"></span>
		</Handle>
	{/each}

	{#each mainOutputs as port, index (port.name)}
		{@const top = portOffset(index, mainOutputs.length)}
		<Handle type="source" id={port.name} position={Position.Right} isConnectable={false} style={`top: ${top}`} aria-label={m.executions_main_output_aria({ name: node.name, port: port.name })}>
			<span class="kf-port"></span>
		</Handle>
		{#if showOutputLabels}
			<span class="pointer-events-none absolute left-full ml-2.5 -translate-y-1/2 font-mono text-[0.625rem] text-muted-foreground" style={`top: ${top}`}>{port.name}</span>
		{/if}
	{/each}

	{#each attachmentInputs as port, index (port.name)}
		{@const left = portOffset(index, attachmentInputs.length)}
		<Handle type="target" id={port.name} position={Position.Bottom} isConnectable={false} style={`left: ${left}`} aria-label={m.executions_attachment_input_aria({ name: node.name, port: port.name })}>
			<span class="kf-port kf-port-attachment"></span>
		</Handle>
		<span class="pointer-events-none absolute top-full -translate-x-1/2 pt-2 font-mono text-[0.625rem] text-muted-foreground" style={`left: ${left}`}>{port.name}</span>
	{/each}

	{#each attachmentOutputs as port, index (port.name)}
		<Handle type="source" id={port.name} position={Position.Top} isConnectable={false} style={`left: ${portOffset(index, attachmentOutputs.length)}`} aria-label={m.executions_attachment_output_aria({ name: node.name, port: port.name })}>
			<span class="kf-port kf-port-attachment"></span>
		</Handle>
	{/each}
</div>
