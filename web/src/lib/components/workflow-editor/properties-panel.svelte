<script lang="ts">
	import { tick } from 'svelte';

	import { goto } from '$app/navigation';
	import { createListCredentialTypes } from '$lib/api/generated/credentials/credentials';
	import { loadNodePropertyOptions, loadNodePropertySchema } from '$lib/api/generated/nodes/nodes';
	import { testCredential } from '$lib/api/generated/credentials/credentials';
	import { createListWorkflowWebhooks, getListWorkflowWebhooksQueryKey } from '$lib/api/generated/workflows/workflows';
	import { propertyVisible, withDefaults } from '$lib/workflow-editor/visibility';
	import type { CredentialResource, CredentialTypeResource, Definition, Node, PropertyDefinition, WebhookRouteResource } from '$lib/api/generated/models';
	import type { PropertyScope } from '$lib/workflow-editor/document';
	import { credentialTypesFor, requiresCredential } from '$lib/workflow-editor/credentials';
	import { savedNodeBinds, webhookAddress } from '$lib/workflow-editor/webhook-address';
	import { Button } from '$lib/components/ui/button';
	import * as m from '$lib/paraglide/messages.js';

	import NodeIcon from './node-icon.svelte';
	import PropertyField from './property-field.svelte';
	import WebhookAddress from './webhook-address.svelte';

	let {
		node,
		definition,
		credentials = [],
		readOnly = false,
		workflowID,
		savedNode = null,
		active = false,
		upstreamNodeNames = [],
		onChange,
		onRename,
		onCredentialChange
	}: {
		node: Node;
		definition: Definition;
		credentials?: CredentialResource[];
		readOnly?: boolean;
		/** The workflow being edited, which is what a webhook node's address is looked up under. */
		workflowID?: string;
		/** This node as the last saved revision holds it, or null when that revision lacks it. */
		savedNode?: Node | null;
		/** Whether the workflow is active, which is when a webhook address starts answering. */
		active?: boolean;
		/** Names of the nodes that run before this one, for `$('Name')` completions. */
		upstreamNodeNames?: string[];
		onChange: (scope: PropertyScope, key: string, value: unknown) => void;
		/** Renames the node and rewrites the expressions that address it. */
		onRename?: (name: string) => void;
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
	const tabButtons: Record<PropertyScope, HTMLButtonElement | undefined> = { parameters: undefined, settings: undefined };

	/** The WAI-ARIA tabs pattern: arrows move between tabs, and focus follows. */
	function moveTab(event: KeyboardEvent, from: PropertyScope) {
		const order: PropertyScope[] = ['parameters', 'settings'];
		const step = event.key === 'ArrowRight' || event.key === 'ArrowDown' ? 1 : event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : 0;
		if (step === 0) return;
		event.preventDefault();
		const next = order[(order.indexOf(from) + step + order.length) % order.length];
		tab = next;
		void tick().then(() => tabButtons[next]?.focus());
	}
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
			if (response.status !== 200) throw new Error(m.credentials_error_test());
			credentialTestResult = { id: credentialID, ok: response.data.ok, detail: response.data.detail ?? '' };
		} catch (error) {
			credentialTestResult = { id: credentialID, ok: false, detail: error instanceof Error ? error.message : m.properties_error_test_not_run() };
		} finally {
			testingCredentialID = null;
		}
	}
	// A webhook node's address comes from the server and nowhere else: the route
	// is minted, so the path on the canvas is not part of it. The lookup waits for
	// a saved node that can bind, because the answer for anything else is empty and
	// the panel already knows why.
	const webhooks = createListWorkflowWebhooks<WebhookRouteResource[]>(() => workflowID ?? '', () => ({
		query: {
			enabled: Boolean(workflowID && definition.webhook && savedNodeBinds(definition.webhook, savedNode)),
			// Keyed by node and never fresh. The route is stable once minted, but a
			// node saved after an earlier answer is not in that answer, and the
			// 30-second default would keep offering the old one.
			queryKey: [...getListWorkflowWebhooksQueryKey(workflowID ?? ''), node.id],
			staleTime: 0,
			select: (response) => {
				if (response.status !== 200) throw new Error(m.properties_webhook_unavailable());
				return response.data ?? [];
			}
		}
	}));
	const webhookView = $derived(
		definition.webhook
			? webhookAddress({
					declaration: definition.webhook,
					node,
					saved: savedNode,
					bindings: webhooks.data,
					failed: webhooks.isError,
					// The origin the editor was served from is the one address known to
					// reach this instance; production serves the API and /webhook there.
					origin: globalThis.location?.origin ?? '',
					active
				})
			: null
	);
	const activeTab = $derived(tab === 'parameters' && (definition.parameters?.length ?? 0) === 0 ? 'settings' : tab);
	const properties = $derived(activeTab === 'parameters' ? definition.parameters ?? [] : definition.sharedSettings ?? []);
	const values = $derived((activeTab === 'parameters' ? node.parameters : node.settings) ?? {});
	/**
	 * What a loader's answer depends on outside the node's parameters.
	 *
	 * Node type and version are part of it because the same loader name can be
	 * registered by two types, and the credential because a model list is
	 * fetched with it.
	 */
	const loaderContext = $derived(`${node.type}@${node.typeVersion}:${Object.values(node.credentials ?? {}).join(',')}`);
	/**
	 * Fetches a property's selectable values from the server.
	 *
	 * The loader itself is never sent — the server takes it from the registered
	 * definition, because everything in this request comes from a browser and
	 * the server makes an outbound call shaped by it.
	 */
	async function loadOptions(property: PropertyDefinition, mode?: string) {
		// A refusal (a non-2xx) never arrives here: apiFetch throws, and the
		// field that asked catches it and shows the server's own message. The
		// `reason` below is only for an answer with no options and no error.
		const response = await loadNodePropertyOptions(node.type, {
			version: String(node.typeVersion ?? ''),
			property: property.key,
			// A resource locator carries a loader per mode rather than one for
			// the property, because "from list" searches and "by ID" does not.
			mode,
			parameters: node.parameters ?? {},
			credentialId: Object.values(node.credentials ?? {})[0]
		});
		if (response.status !== 200) return { options: [], reason: m.properties_error_options_unavailable() };
		return { options: response.data.options ?? [], reason: response.data.reason ?? '' };
	}

	/** A resource mapper's columns. Its own call, for the reason above. */
	async function loadSchema(property: PropertyDefinition) {
		// Same contract as loadOptions: failures reach the field as a throw.
		const response = await loadNodePropertySchema(node.type, {
			version: String(node.typeVersion ?? ''),
			property: property.key,
			parameters: node.parameters ?? {},
			credentialId: Object.values(node.credentials ?? {})[0]
		});
		if (response.status !== 200) return { fields: [], reason: m.properties_error_columns_unavailable() };
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

<section aria-label={m.properties_panel_aria({ name: node.name })} class="flex h-full min-h-0 flex-col bg-card">
	<div class="flex shrink-0 items-center gap-2 border-b border-border px-2.5 py-2">
		<NodeIcon {definition} size="md" label={m.properties_node_icon_label({ category: definition.category })} />
		<div class="min-w-0 flex-1">
			{#if onRename && !readOnly}
				<!-- The title is where n8n renames from, and the name is what
				     expressions address: an imported node whose name cannot be
				     changed is a node nothing can safely reference. -->
				<label class="sr-only" for={`node-name-${node.id}`}>{m.properties_node_name()}</label>
				<input
					id={`node-name-${node.id}`}
					class="w-full truncate rounded border border-transparent bg-transparent text-[0.8125rem] font-semibold leading-tight hover:border-border focus-visible:border-primary focus-visible:outline-none"
					value={node.name}
					onkeydown={(event) => {
						if (event.key === 'Enter') event.currentTarget.blur();
						if (event.key === 'Escape') {
							event.currentTarget.value = node.name;
							event.currentTarget.blur();
						}
					}}
					onchange={(event) => onRename?.(event.currentTarget.value)}
				/>
			{:else}
				<h2 class="truncate text-[0.8125rem] font-semibold leading-tight">{node.name}</h2>
			{/if}
			<p class="truncate font-mono text-[0.625rem] leading-tight text-muted-foreground">{definition.type}</p>
		</div>
		{#if definition.documentationUrl}
			<a href={definition.documentationUrl} target="_blank" rel="noreferrer" class="shrink-0 rounded-md border border-border px-1.5 py-0.5 text-[0.625rem] font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring">{m.properties_docs()}</a>
		{/if}
	</div>

	<div class="flex shrink-0 gap-3 border-b border-border px-2.5" role="tablist" tabindex="-1" aria-label={m.properties_node_configuration()} onkeydown={(event) => {
		if (event.key === 'ArrowRight' || event.key === 'ArrowLeft' || event.key === 'ArrowUp' || event.key === 'ArrowDown') moveTab(event, tab);
	}}>
		<button bind:this={tabButtons.parameters} type="button" role="tab" id="node-tab-parameters" aria-controls="node-tabpanel" aria-selected={activeTab === 'parameters'} tabindex={activeTab === 'parameters' ? 0 : -1} class="-mb-px border-b-2 border-transparent py-1.5 text-xs text-muted-foreground transition-colors aria-selected:border-primary aria-selected:font-medium aria-selected:text-foreground" onclick={() => (tab = 'parameters')}>{m.properties_tab_parameters()}</button>
		<button bind:this={tabButtons.settings} type="button" role="tab" id="node-tab-settings" aria-controls="node-tabpanel" aria-selected={activeTab === 'settings'} tabindex={activeTab === 'settings' ? 0 : -1} class="-mb-px border-b-2 border-transparent py-1.5 text-xs text-muted-foreground transition-colors aria-selected:border-primary aria-selected:font-medium aria-selected:text-foreground" onclick={() => (tab = 'settings')}>{m.nav_settings()}</button>
	</div>

	<!-- `inert` rather than a pointer-events class: a keyboard user could tab
	     into a read-only field and type text that was silently discarded. It
	     covers the editable fields only. The webhook address is something to
	     read and copy, and a read-only viewer needs it as much as an editor. -->
	<div class="min-h-0 flex-1 space-y-3 overflow-y-auto p-2.5" role="tabpanel" id="node-tabpanel" aria-labelledby={`node-tab-${activeTab}`}>
		{#if activeTab === 'parameters' && webhookView}
			<WebhookAddress address={webhookView} onRetry={() => void webhooks.refetch()} />
		{/if}
		<div class="space-y-3" inert={readOnly} class:opacity-70={readOnly}>
			{#if activeTab === 'parameters' && applicableCredentialTypes.length > 0 && onCredentialChange}
				<div class="grid gap-1.5 rounded-lg border border-border bg-background/40 p-2">
					<p class="text-[0.6875rem] font-medium uppercase tracking-wider text-muted-foreground">
						{m.properties_credential()}{#if credentialRequired}<span class="text-destructive" aria-hidden="true">*</span><span class="sr-only">{m.properties_credential_required()}</span>{/if}
					</p>
					{#if credentialRequired && !Object.keys(node.credentials ?? {}).length}
						<p class="text-[0.6875rem] leading-4 text-destructive">{m.properties_credential_needed()}</p>
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
								<option value="">{m.properties_credential_none()}</option>
								{#each matching as candidate (candidate.id)}
									<option value={candidate.id}>{candidate.name}</option>
								{/each}
							</select>
							{#if selectedCredential(typeID)}
								<Button variant="outline" size="sm" class="h-7 shrink-0 px-2 text-[0.6875rem]" disabled={testingCredentialID !== null} onclick={() => void testSelectedCredential(typeID)}>
									{testingCredentialID ? m.credentials_testing() : m.credentials_test()}
								</Button>
							{/if}
						</div>
						{#if matching.length === 0}
							<p class="text-[0.625rem] leading-4 text-muted-foreground">{m.properties_no_credential_yet({ type: credentialTypeName(typeID) })}<button type="button" class="underline underline-offset-2" onclick={() => void goto('/credentials')}>{m.properties_add_one_under_credentials()}</button>.</p>
						{:else if credentialTestResult && credentialTestResult.id === selectedCredential(typeID)}
							<p role="status" class={`text-[0.625rem] leading-4 ${credentialTestResult.ok ? 'text-success' : 'text-destructive'}`}>{credentialTestResult.ok ? (credentialTestResult.detail ? m.credentials_test_connected_detail({ detail: credentialTestResult.detail }) : m.credentials_test_connected()) : m.credentials_test_failed({ detail: credentialTestResult.detail })}</p>
						{/if}
					{/each}
					<p class="text-[0.625rem] leading-4 text-muted-foreground">{m.properties_credential_reference_note()}</p>
				</div>
			{/if}
			{#if visibleProperties.length === 0}
				<p class="text-xs leading-5 text-muted-foreground">{activeTab === 'parameters' ? m.properties_no_parameters_to_configure() : m.properties_no_settings_to_configure()}</p>
			{:else}
				{#each visibleProperties as property (property.key)}
					<PropertyField {property} value={values[property.key] ?? property.default} siblings={values} contextKey={loaderContext} ownerKey={node.id} {upstreamNodeNames} onChange={(value) => onChange(activeTab, property.key, value)} loadOptions={activeTab === 'parameters' ? loadOptions : undefined} loadSchema={activeTab === 'parameters' ? loadSchema : undefined} />
				{/each}
			{/if}
		</div>
	</div>
	<footer class="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-0.5 border-t border-border px-2.5 py-1.5 text-[0.625rem] leading-4 text-muted-foreground">
		<span class="truncate">{m.properties_footer_version({ name: definition.displayName, version: definition.version })}</span>
		{#if definition.description}<span class="min-w-0 flex-1 truncate" title={definition.description}>{definition.description}</span>{/if}
	</footer>
</section>
