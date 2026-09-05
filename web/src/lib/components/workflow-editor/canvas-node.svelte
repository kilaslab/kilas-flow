<script lang="ts">
	import { Handle, Position, type NodeProps } from '@xyflow/svelte';

	import type { EditorFlowNode } from '$lib/workflow-editor/document';

	let { data, selected }: NodeProps<EditorFlowNode> = $props();

	const inputs = $derived(data.definition.inputs ?? []);
	const outputs = $derived(data.definition.outputs ?? []);
	const validationMessage = $derived(data.validationMessage);

	function handleOffset(index: number, count: number): string {
		return `${((index + 1) / (count + 1)) * 100}%`;
	}
</script>

<div class:!border-primary={selected} class:!border-destructive={Boolean(validationMessage)} class="w-64 rounded-xl border border-border bg-card shadow-sm transition-shadow data-[selected=true]:shadow-md" data-selected={selected} aria-invalid={Boolean(validationMessage)} aria-describedby={validationMessage ? `node-validation-${data.workflowNode.id}` : undefined}>
	<div class="border-b border-border px-3 py-2.5">
		<p class="text-xs font-medium text-muted-foreground">{data.definition.category}</p>
		<p class="mt-0.5 truncate text-sm font-semibold">{data.workflowNode.name}</p>
	</div>
	<div class="px-3 py-2 text-xs text-muted-foreground">{data.definition.description || data.definition.displayName}</div>
	{#if validationMessage}
		<p id={`node-validation-${data.workflowNode.id}`} class="border-t border-destructive/20 bg-destructive/5 px-3 py-2 text-xs leading-5 text-destructive">{validationMessage}</p>
	{/if}

	{#each inputs as port, index (port.Name)}
		<Handle type="target" id={port.Name} position={Position.Left} style={`top: ${handleOffset(index, inputs.length)}`} aria-label={`${data.workflowNode.name} input ${port.Name} (${port.Kind})`} />
		<span class="pointer-events-none absolute left-2 -translate-y-1/2 text-[10px] text-muted-foreground" style={`top: ${handleOffset(index, inputs.length)}`}>{port.Name}</span>
	{/each}

	{#each outputs as port, index (port.Name)}
		<Handle type="source" id={port.Name} position={Position.Right} style={`top: ${handleOffset(index, outputs.length)}`} aria-label={`${data.workflowNode.name} output ${port.Name} (${port.Kind})`} />
		<span class="pointer-events-none absolute right-2 -translate-y-1/2 text-[10px] text-muted-foreground" style={`top: ${handleOffset(index, outputs.length)}`}>{port.Name}</span>
	{/each}
</div>
