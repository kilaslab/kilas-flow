<script lang="ts">
	import { Handle, Position, type NodeProps } from '@xyflow/svelte';

	import type { EditorFlowNode } from '$lib/workflow-editor/document';
	import { statusLabel, statusTone } from '$lib/workflow-editor/execution';

	// The replay node mirrors the editor node's ports so a graph reads the same
	// in both views, but it renders a run status instead of validation state and
	// exposes no editing affordance at all.
	let { data, selected }: NodeProps<EditorFlowNode> = $props();

	const inputs = $derived(data.definition.inputs ?? []);
	const outputs = $derived(data.definition.outputs ?? []);
	const status = $derived(data.runStatus ?? 'skipped');

	function handleOffset(index: number, count: number): string {
		return `${((index + 1) / (count + 1)) * 100}%`;
	}
</script>

<div
	class:!border-primary={selected}
	class:opacity-70={status === 'skipped'}
	class="w-64 rounded-xl border border-border bg-card shadow-sm"
	data-selected={selected}
	data-run-status={status}
>
	<div class="flex items-start gap-2 border-b border-border px-3 py-2.5">
		<div class="min-w-0 flex-1">
			<p class="text-xs font-medium text-muted-foreground">{data.definition.category}</p>
			<p class="mt-0.5 truncate text-sm font-semibold">{data.workflowNode.name}</p>
		</div>
		<span class={`shrink-0 rounded-full border px-2 py-0.5 text-[10px] font-medium ${statusTone(status)}`}>{statusLabel(status)}</span>
	</div>
	<div class="px-3 py-2 text-xs text-muted-foreground">{data.definition.description || data.definition.displayName}</div>

	{#each inputs as port, index (port.Name)}
		<Handle type="target" id={port.Name} position={Position.Left} isConnectable={false} style={`top: ${handleOffset(index, inputs.length)}`} aria-label={`${data.workflowNode.name} input ${port.Name} (${port.Kind})`} />
		<span class="pointer-events-none absolute left-2 -translate-y-1/2 text-[10px] text-muted-foreground" style={`top: ${handleOffset(index, inputs.length)}`}>{port.Name}</span>
	{/each}

	{#each outputs as port, index (port.Name)}
		<Handle type="source" id={port.Name} position={Position.Right} isConnectable={false} style={`top: ${handleOffset(index, outputs.length)}`} aria-label={`${data.workflowNode.name} output ${port.Name} (${port.Kind})`} />
		<span class="pointer-events-none absolute right-2 -translate-y-1/2 text-[10px] text-muted-foreground" style={`top: ${handleOffset(index, outputs.length)}`}>{port.Name}</span>
	{/each}
</div>
