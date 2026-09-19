<script lang="ts">
	import { goto } from '$app/navigation';
	import { createListCredentialTypes } from '$lib/api/generated/credentials/credentials';
	import { loadNodePropertyOptions, loadNodePropertySchema } from '$lib/api/generated/nodes/nodes';
	import { testCredential } from '$lib/api/generated/credentials/credentials';
	import { propertyVisible, withDefaults } from '$lib/workflow-editor/visibility';
	import type { CredentialResource, CredentialTypeResource, Definition, Node, PropertyDefinition } from '$lib/api/generated/models';
	import type { PropertyScope } from '$lib/workflow-editor/document';
	import { credentialTypesFor, requiresCredential } from '$lib/workflow-editor/credentials';
	import { Button } from '$lib/components/ui/button';

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

	// Display names for credential type ids. Loaded here rather than passed
	// down: the panel is rendered by the canvas editor owned elsewhere, and
	// threading a prop through it would touch files outside this slice.
	const credentialTypeList = createListCredentialTypes<CredentialTypeResource[]>(() => ({
		query: {
			select: (response) => (response.status === 200 ? (response.data ?? []) : [])
		}
	}));

	// Which credential types this node can authenticate with is derived from the
	// node type, so the panel stays generic and gains new types for free.
	// The `visibleWhen` gating lives in credentialTypesFor: a webhook with
	// Authentication None shows no picker at all, not two raw-id selects.
	const applicableCredentialTypes = $derived(credentialTypesFor(definition, (node.parameters ?? {}) as Record<string, unknown>));
	const credentialRequired = $derived(requiresCredential(definition));
	const selectedCredential = $derived((typeID: string) => node.credentials?.[typeID] ?? '');
	let tab = $state<PropertyScope>('parameters');
	/** Display name for a credential type id; falls back to the raw id. */
	function credentialTypeName(typeID: string): string {
		return (credentialTypeList.data ?? []).find((candidate) => candidate.id === typeID)?.displayName ?? typeID;
	}
	let testingCredentialID = $state<string | null>(null);
	let credentialTestResult = $state<{ id: string; ok: boolean; detail: string } | null>(null);

	async function testSelectedCredential(typeID: string) {
		const credentialID = selectedCredential(typeID);
		if (!credentialID || testingCredentialID) return;
		testingCredentialID = credentialID;
		credentialTestResult = null;
		try {
			const response = await testCredential(credentialID);
			if (response.status !== 200) throw new Error('Unexpected credential-test response');
			credentialTestResult = { id: credentialID, ok: response.data.ok, detail: response.data.detail ?? '' };
		} catch (error) {
			credentialTestResult = { id: credentialID, ok: false, detail: error instanceof Error ? error.message : 'The test could not run.' };
		} finally {
			testingCredentialID = null;
		}
	}
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
		{#if definition.documentationUrl}
			<a href={definition.documentationUrl} target="_blank" rel="noreferrer" class="shrink-0 rounded-md border border-border px-1.5 py-0.5 text-[0.625rem] font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring">Docs</a>
		{/if}
	</div>

	<div class="flex shrink-0 gap-3 border-b border-border px-2.5" role="tablist" aria-label="Node configuration">
		<button type="button" role="tab" id="node-tab-parameters" aria-controls="node-tabpanel" aria-selected={activeTab === 'parameters'} class="-mb-px border-b-2 border-transparent py-1.5 text-xs text-muted-foreground transition-colors aria-selected:border-primary aria-selected:font-medium aria-selected:text-foreground" onclick={() => (tab = 'parameters')}>Parameters</button>
		<button type="button" role="tab" id="node-tab-settings" aria-controls="node-tabpanel" aria-selected={activeTab === 'settings'} class="-mb-px border-b-2 border-transparent py-1.5 text-xs text-muted-foreground transition-colors aria-selected:border-primary aria-selected:font-medium aria-selected:text-foreground" onclick={() => (tab = 'settings')}>Settings</button>
	</div>

	<div class="min-h-0 flex-1 space-y-3 overflow-y-auto p-2.5" class:pointer-events-none={readOnly} class:opacity-70={readOnly} role="tabpanel" id="node-tabpanel" aria-labelledby={`node-tab-${activeTab}`}>
		{#if activeTab === 'parameters' && definition.webhook}
			{@const pathParam = definition.webhook.pathParameter ? String((node.parameters as Record<string, unknown> | undefined)?.[definition.webhook.pathParameter] ?? '') : definition.webhook.staticPath ?? ''}
			<div class="grid gap-1.5 rounded-lg border border-border bg-background/40 p-2">
				<p class="text-[0.6875rem] font-medium uppercase tracking-wider text-muted-foreground">Webhook URL</p>
				{#if pathParam}
					<code class="truncate rounded border border-border bg-muted/40 px-1.5 py-1 font-mono text-[0.6875rem] select-all" title={`/webhook/${pathParam}`}>{`/webhook/${pathParam}`}</code>
					<p class="text-[0.625rem] leading-4 text-muted-foreground">The full address is shown after import and on activation. Prefix it with your host when pointing the sender at it.</p>
				{:else}
					<p class="text-[0.625rem] leading-4 text-muted-foreground">Set the path below — the public URL is minted from it on activation.</p>
				{/if}
			</div>
		{/if}
		{#if activeTab === 'parameters' && applicableCredentialTypes.length > 0 && onCredentialChange}
			<div class="grid gap-1.5 rounded-lg border border-border bg-background/40 p-2">
				<p class="text-[0.6875rem] font-medium uppercase tracking-wider text-muted-foreground">
					Credential{#if credentialRequired}<span class="text-destructive" aria-hidden="true">*</span><span class="sr-only"> (required)</span>{/if}
				</p>
				{#if credentialRequired && !Object.keys(node.credentials ?? {}).length}
					<p class="text-[0.6875rem] leading-4 text-destructive">This node needs a credential before it can run.</p>
				{/if}
				{#each applicableCredentialTypes as typeID (typeID)}
					{@const matching = credentials.filter((candidate) => candidate.type === typeID)}
					<label class="text-[0.6875rem] font-medium" for={`credential-${typeID}`}>{credentialTypeName(typeID)}</label>
					<div class="flex items-center gap-1.5">
						<select
							id={`credential-${typeID}`}
							value={selectedCredential(typeID)}
							class="h-7 min-w-0 flex-1 rounded-md border border-input bg-background px-1.5 text-xs"
							onchange={(event) => onCredentialChange?.(typeID, event.currentTarget.value)}
						>
							<option value="">None</option>
							{#each matching as candidate (candidate.id)}
								<option value={candidate.id}>{candidate.name}</option>
							{/each}
						</select>
						{#if selectedCredential(typeID)}
							<Button variant="outline" size="sm" class="h-7 shrink-0 px-2 text-[0.6875rem]" disabled={testingCredentialID !== null} onclick={() => void testSelectedCredential(typeID)}>
								{testingCredentialID ? 'Testing…' : 'Test'}
							</Button>
						{/if}
					</div>
					{#if matching.length === 0}
						<p class="text-[0.625rem] leading-4 text-muted-foreground">No {credentialTypeName(typeID)} credential yet — <button type="button" class="underline underline-offset-2" onclick={() => void goto('/credentials')}>add one under Credentials</button>.</p>
					{:else if credentialTestResult && credentialTestResult.id === selectedCredential(typeID)}
						<p role="status" class={`text-[0.625rem] leading-4 ${credentialTestResult.ok ? 'text-success' : 'text-destructive'}`}>{credentialTestResult.ok ? `Connected${credentialTestResult.detail ? ` — ${credentialTestResult.detail}` : ''}` : `Test failed — ${credentialTestResult.detail}`}</p>
					{/if}
				{/each}
				<p class="text-[0.625rem] leading-4 text-muted-foreground">The workflow records only the reference. Secrets stay in credential storage.</p>
			</div>
		{/if}
		{#if visibleProperties.length === 0}
			<p class="text-xs leading-5 text-muted-foreground">This node has no {activeTab === 'parameters' ? 'parameters' : 'shared settings'} to configure.</p>
		{:else}
			{#each visibleProperties as property (property.key)}
				<PropertyField {property} value={values[property.key] ?? property.default} siblings={values} onChange={(value) => onChange(activeTab, property.key, value)} loadOptions={activeTab === 'parameters' ? loadOptions : undefined} loadSchema={activeTab === 'parameters' ? loadSchema : undefined} />
			{/each}
		{/if}
	</div>
	<footer class="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-0.5 border-t border-border px-2.5 py-1.5 text-[0.625rem] leading-4 text-muted-foreground">
		<span class="truncate">{definition.displayName} version {definition.version}</span>
		{#if definition.description}<span class="min-w-0 flex-1 truncate" title={definition.description}>{definition.description}</span>{/if}
	</footer>
</section>
