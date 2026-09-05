<script lang="ts">
	import { Handle, Position, type NodeProps } from '@xyflow/svelte';
	import Check from '@lucide/svelte/icons/check';
	import LoaderCircle from '@lucide/svelte/icons/loader-circle';
	import Minus from '@lucide/svelte/icons/minus';
	import X from '@lucide/svelte/icons/x';

	import type { EditorFlowNode } from '$lib/workflow-editor/document';
	import { statusAccent, statusLabel } from '$lib/workflow-editor/execution';
	import { attachmentPorts, mainPorts, nodeSubtitle, nodeVisual } from '$lib/workflow-editor/node-visual';

	/**
	 * The replay node keeps the editor node's geometry and ports so a graph reads
	 * the same in both views. What changes is what the border means: here it
	 * carries the recorded run status rather than the node's category, because
	 * that is the one thing a reader came to this view for.
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

	const TILE = {
		trigger: 'h-22 w-22 rounded-l-[2.75rem] rounded-r-xl',
		step: 'h-22 w-22 rounded-xl',
		hub: 'h-18 min-w-44 gap-2.5 rounded-2xl px-4',
		attachment: 'size-15 rounded-full'
	} as const;

	const BADGE = { succeeded: Check, failed: X, running: LoaderCircle, cancelled: X, cancelling: X } as const;

	function offset(index: number, count: number): string {
		return `${((index + 1) / (count + 1)) * 100}%`;
	}
</script>

<div class="relative" style={`--accent: ${accent}`} data-run-status={status}>
	<div
		class="flex items-center justify-center border bg-card {TILE[visual.shape]}"
		class:opacity-60={!reached}
		style={`border-color: ${reached ? 'var(--accent)' : 'var(--border)'}; box-shadow: ${
			selected ? '0 0 0 2px color-mix(in oklch, var(--accent) 35%, transparent)' : 'none'
		}`}
	>
		<visual.icon
			class={visual.shape === 'step' || visual.shape === 'trigger' ? 'size-7' : 'size-5'}
			style={`color: ${reached ? 'var(--accent)' : 'var(--muted-foreground)'}`}
			aria-hidden="true"
		/>
		{#if visual.shape === 'hub'}
			<span class="truncate text-[0.8125rem] font-semibold leading-tight">{node.name}</span>
		{/if}
	</div>

	{#if reached}
		{@const Badge = BADGE[status as keyof typeof BADGE]}
		<span
			class="absolute -bottom-1 -right-1 grid size-4.5 place-items-center rounded-full border-2 border-background"
			style="background: var(--accent)"
			title={statusLabel(status)}
		>
			{#if Badge}
				<Badge aria-hidden="true" class="size-2.5 text-background {status === 'running' ? 'animate-spin' : ''}" />
			{:else}
				<Minus aria-hidden="true" class="size-2.5 text-background" />
			{/if}
		</span>
	{/if}
	<span class="sr-only">{statusLabel(status)}</span>

	<div class="pointer-events-none absolute left-1/2 top-full w-40 -translate-x-1/2 pt-1.5 text-center">
		{#if visual.shape !== 'hub'}
			<p class="truncate text-[0.8125rem] font-semibold leading-tight" class:text-muted-foreground={!reached}>{node.name}</p>
		{/if}
		{#if subtitle}
			<p class="truncate pt-0.5 font-mono text-[0.6875rem] leading-tight text-muted-foreground">{subtitle}</p>
		{/if}
	</div>

	{#each mainInputs as port, index (port.Name)}
		<Handle type="target" id={port.Name} position={Position.Left} isConnectable={false} style={`top: ${offset(index, mainInputs.length)}`} aria-label={`${node.name} input ${port.Name}`}>
			<span class="kf-port"></span>
		</Handle>
	{/each}

	{#each mainOutputs as port, index (port.Name)}
		{@const top = offset(index, mainOutputs.length)}
		<Handle type="source" id={port.Name} position={Position.Right} isConnectable={false} style={`top: ${top}`} aria-label={`${node.name} output ${port.Name}`}>
			<span class="kf-port"></span>
		</Handle>
		{#if showOutputLabels}
			<span class="pointer-events-none absolute left-full ml-2.5 -translate-y-1/2 font-mono text-[0.625rem] text-muted-foreground" style={`top: ${top}`}>{port.Name}</span>
		{/if}
	{/each}

	{#each attachmentInputs as port, index (port.Name)}
		{@const left = offset(index, attachmentInputs.length)}
		<Handle type="target" id={port.Name} position={Position.Bottom} isConnectable={false} style={`left: ${left}`} aria-label={`${node.name} ${port.Name} attachment`}>
			<span class="kf-port kf-port-attachment"></span>
		</Handle>
		<span class="pointer-events-none absolute top-full -translate-x-1/2 pt-2 font-mono text-[0.625rem] text-muted-foreground" style={`left: ${left}`}>{port.Name}</span>
	{/each}

	{#each attachmentOutputs as port, index (port.Name)}
		<Handle type="source" id={port.Name} position={Position.Top} isConnectable={false} style={`left: ${offset(index, attachmentOutputs.length)}`} aria-label={`${node.name} provides ${port.Name}`}>
			<span class="kf-port kf-port-attachment"></span>
		</Handle>
	{/each}
</div>
