<script lang="ts">
	import { loadNodePropertyOptions, loadNodePropertySchema } from '$lib/api/generated/nodes/nodes';
	import { propertyVisible, withDefaults } from '$lib/workflow-editor/visibility';
	import type { CredentialResource, Definition, Node, PropertyDefinition } from '$lib/api/generated/models';
	import type { PropertyScope } from '$lib/workflow-editor/document';
	import { credentialTypesFor, requiresCredential } from '$lib/workflow-editor/credentials';

	import NodeIcon from './node-icon.svelte';
	import PropertyField from './property-field.svelte';

	let {
		node,
		definition,
		credentials = [],
		readOnly = false,
		onChange,
		onCredentialChange
	}: {
		node: Node;
		definition: Definition;
		credentials?: CredentialResource[];
		readOnly?: boolean;
		onChange: (scope: PropertyScope, key: string, value: unknown) => void;
		onCredentialChange?: (typeID: string, credentialID: string) => void;
	} = $props();

	// Which credential types this node can authenticate with is derived from the
	// node type, so the panel stays generic and gains new types for free.
	const credentialTypes = $derived(credentialTypesFor(definition));
	const credentialRequired = $derived(requiresCredential(definition));
	const selectedCredential = $derived((typeID: string) => node.credentials?.[typeID] ?? '');
	let tab = $state<PropertyScope>('parameters');
	const activeTab = $derived(tab === 'parameters' && (definition.parameters?.length ?? 0) === 0 ? 'settings' : tab);
	const properties = $derived(activeTab === 'parameters' ? definition.parameters ?? [] : definition.sharedSettings ?? []);
	const values = $derived((activeTab === 'parameters' ? node.parameters : node.settings) ?? {});
	/**
	 * Fetches a property's selectable values from the server.
	 *
	 * The loader itself is never sent — the server takes it from the registered
	 * definition, because everything in this request comes from a browser and
	 * the server makes an outbound call shaped by it.
	 */
	async function loadOptions(property: PropertyDefinition, mode?: string) {
		const response = await loadNodePropertyOptions(node.type, {
			version: String(node.typeVersion ?? ''),
			property: property.key,
			// A resource locator carries a loader per mode rather than one for
			// the property, because "from list" searches and "by ID" does not.
			mode,
			parameters: node.parameters ?? {},
			credentialId: Object.values(node.credentials ?? {})[0]
		});
		if (response.status !== 200) return { options: [], reason: 'These options could not be loaded.' };
		return { options: response.data.options ?? [], reason: response.data.reason ?? '' };
	}

	/** A resource mapper's columns. Its own call, for the reason above. */
	async function loadSchema(property: PropertyDefinition) {
		const response = await loadNodePropertySchema(node.type, {
			version: String(node.typeVersion ?? ''),
			property: property.key,
			parameters: node.parameters ?? {},
			credentialId: Object.values(node.credentials ?? {})[0]
		});
		if (response.status !== 200) return { fields: [], reason: 'These columns could not be loaded.' };
		return { fields: response.data.fields ?? [], reason: response.data.reason ?? '' };
	}

	// The rule lives in one module, ported from the Go evaluator and checked
	// against the same fixture. The previous inline version was single-value,
	// AND-only, show-only and used strict equality, so a condition on anything
	// but a primitive was silently always false.
	const visibleProperties = $derived(
		properties.filter((property) =>
			propertyVisible(property, withDefaults(properties, values), String(node.typeVersion ?? ''))
		)
	);
</script>

<section aria-label={`${node.name} properties`} class="flex h-full min-h-0 flex-col bg-card">
	<div class="flex shrink-0 items-center gap-2 border-b border-border px-2.5 py-2">
		<NodeIcon {definition} size="md" label={`${definition.category} node`} />
		<div class="min-w-0 flex-1">
			<h2 class="truncate text-[0.8125rem] font-semibold leading-tight">{node.name}</h2>
			<p class="truncate font-mono text-[0.625rem] leading-tight text-muted-foreground">{definition.type}</p>
		</div>
	</div>

	<div class="flex shrink-0 gap-3 border-b border-border px-2.5" role="tablist" aria-label="Node configuration">
		<button type="button" role="tab" id="node-tab-parameters" aria-controls="node-tabpanel" aria-selected={activeTab === 'parameters'} class="-mb-px border-b-2 border-transparent py-1.5 text-xs text-muted-foreground transition-colors aria-selected:border-primary aria-selected:font-medium aria-selected:text-foreground" onclick={() => (tab = 'parameters')}>Parameters</button>
		<button type="button" role="tab" id="node-tab-settings" aria-controls="node-tabpanel" aria-selected={activeTab === 'settings'} class="-mb-px border-b-2 border-transparent py-1.5 text-xs text-muted-foreground transition-colors aria-selected:border-primary aria-selected:font-medium aria-selected:text-foreground" onclick={() => (tab = 'settings')}>Settings</button>
	</div>

	<div class="min-h-0 flex-1 space-y-3 overflow-y-auto p-2.5" class:pointer-events-none={readOnly} class:opacity-70={readOnly} role="tabpanel" id="node-tabpanel" aria-labelledby={`node-tab-${activeTab}`}>
		{#if activeTab === 'parameters' && credentialTypes.length > 0 && onCredentialChange}
			<div class="grid gap-1.5 rounded-lg border border-border bg-background/40 p-2">
				<p class="text-[0.6875rem] font-medium uppercase tracking-wider text-muted-foreground">
					Credential{#if credentialRequired}<span class="text-destructive" aria-hidden="true">*</span><span class="sr-only"> (required)</span>{/if}
				</p>
				{#if credentialRequired && !Object.keys(node.credentials ?? {}).length}
					<!-- A node that cannot run without a credential deserves a
					     visible prompt rather than a silently empty select. -->
					<p class="text-[0.6875rem] leading-4 text-destructive">This node needs a credential before it can run.</p>
				{/if}
				{#each credentialTypes as typeID (typeID)}
					{@const matching = credentials.filter((candidate) => candidate.type === typeID)}
					<label class="font-mono text-[0.625rem] text-muted-foreground" for={`credential-${typeID}`}>{typeID}</label>
					<select
						id={`credential-${typeID}`}
						value={selectedCredential(typeID)}
						class="h-7 rounded-md border border-input bg-background px-1.5 text-xs"
						onchange={(event) => onCredentialChange?.(typeID, event.currentTarget.value)}
					>
						<option value="">None</option>
						{#each matching as candidate (candidate.id)}
							<option value={candidate.id}>{candidate.name}</option>
						{/each}
					</select>
					{#if matching.length === 0}
						<p class="text-[0.625rem] leading-4 text-muted-foreground">No {typeID} credential yet — add one under Credentials.</p>
					{/if}
				{/each}
				<p class="text-[0.625rem] leading-4 text-muted-foreground">The workflow records only the reference. Secrets stay in credential storage.</p>
			</div>
		{/if}
		{#if visibleProperties.length === 0}
			<p class="text-xs leading-5 text-muted-foreground">This node has no {activeTab === 'parameters' ? 'parameters' : 'shared settings'} to configure.</p>
		{:else}
			{#each visibleProperties as property (property.key)}
				<PropertyField {property} value={values[property.key]} onChange={(value) => onChange(activeTab, property.key, value)} loadOptions={activeTab === 'parameters' ? loadOptions : undefined} loadSchema={activeTab === 'parameters' ? loadSchema : undefined} />
			{/each}
		{/if}
	</div>
</section>
