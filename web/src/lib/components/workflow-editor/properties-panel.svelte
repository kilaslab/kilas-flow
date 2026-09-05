<script lang="ts">
	import type { Definition, Node, PropertyDefinition } from '$lib/api/generated/models';
	import type { PropertyScope } from '$lib/workflow-editor/document';

	import PropertyField from './property-field.svelte';

	let { node, definition, onChange }: { node: Node; definition: Definition; onChange: (scope: PropertyScope, key: string, value: unknown) => void } = $props();
	let tab = $state<PropertyScope>('parameters');
	const activeTab = $derived(tab === 'parameters' && (definition.parameters?.length ?? 0) === 0 ? 'settings' : tab);
	const properties = $derived(activeTab === 'parameters' ? definition.parameters ?? [] : definition.sharedSettings ?? []);
	const values = $derived((activeTab === 'parameters' ? node.parameters : node.settings) ?? {});
	const visibleProperties = $derived(properties.filter((property) => isVisible(property, values)));

	function isVisible(property: PropertyDefinition, current: Record<string, unknown>): boolean {
		return (property.visibleWhen ?? []).every((condition) => current[condition.key] === condition.equals);
	}
</script>

<section aria-label={`${node.name} properties`} class="flex min-h-0 flex-col bg-card">
	<div class="border-b border-border px-4 py-3">
		<p class="text-xs font-medium text-muted-foreground">{definition.category}</p>
		<h2 class="mt-0.5 truncate text-base font-semibold">{node.name}</h2>
	</div>
	<div class="flex border-b border-border px-2" role="tablist" aria-label="Node configuration">
		<button type="button" role="tab" aria-selected={activeTab === 'parameters'} class:font-semibold={activeTab === 'parameters'} class="border-b-2 border-transparent px-3 py-2 text-sm aria-selected:border-primary" onclick={() => (tab = 'parameters')}>Parameters</button>
		<button type="button" role="tab" aria-selected={activeTab === 'settings'} class:font-semibold={activeTab === 'settings'} class="border-b-2 border-transparent px-3 py-2 text-sm aria-selected:border-primary" onclick={() => (tab = 'settings')}>Settings</button>
	</div>
	<div class="min-h-0 flex-1 space-y-5 overflow-y-auto p-4" role="tabpanel">
		{#if visibleProperties.length === 0}
			<p class="text-sm leading-6 text-muted-foreground">This node has no {activeTab === 'parameters' ? 'parameters' : 'shared settings'} to configure.</p>
		{:else}
			{#each visibleProperties as property (property.key)}
				<PropertyField {property} value={values[property.key]} onChange={(value) => onChange(activeTab, property.key, value)} />
			{/each}
		{/if}
	</div>
</section>
