<script lang="ts">
	import { tick } from 'svelte';
	import '@xyflow/svelte/dist/style.css';

	import { Background, BackgroundVariant, Controls, SvelteFlow, type Connection as FlowConnection } from '@xyflow/svelte';
	import PanelLeftClose from '@lucide/svelte/icons/panel-left-close';
	import Plus from '@lucide/svelte/icons/plus';
	import Play from '@lucide/svelte/icons/play';
	import Save from '@lucide/svelte/icons/save';
	import Trash2 from '@lucide/svelte/icons/trash-2';

	import type { Definition, Document, WorkflowDocumentInput } from '$lib/api/generated/models';
	import {
		cloneWorkflowDocument,
		createWorkflowNode,
		documentFromCanvas,
		documentFromFlow,
		nextNodePosition,
		toWorkflowInput,
		updateNodeProperty,
		workflowDocumentEquals,
		type EditorFlowEdge,
		type EditorFlowNode,
		type PropertyScope
	} from '$lib/workflow-editor/document';
	import { mediaQuery } from '$lib/workflow-editor/media.svelte';
	import { canConnect, connectionFromCanvas } from '$lib/workflow-editor/ports';
	import type { CanvasValidationIssue } from '$lib/workflow-editor/validation';

	import CanvasNode from './canvas-node.svelte';
	import NodePicker from './node-picker.svelte';
	import PropertiesPanel from './properties-panel.svelte';

	let {
		document,
		definitions,
		saving = false,
		running = false,
		saveError = null,
		saveIssues = [],
		runError = null,
		runMessage = null,
		onSave,
		onRun
	}: {
		document: Document;
		definitions: Definition[];
		saving?: boolean;
		running?: boolean;
		saveError?: string | null;
		saveIssues?: CanvasValidationIssue[];
		runError?: string | null;
		runMessage?: string | null;
		onSave: (input: WorkflowDocumentInput) => Promise<void>;
		onRun: () => Promise<void>;
	} = $props();

	const nodeTypes = { workflow: CanvasNode };
	// Matches the `lg:` breakpoint the layout below switches on.
	const narrow = mediaQuery('(max-width: 1023.98px)');
	function initialCanvasState() {
		return documentFromCanvas(document, definitions);
	}
	let draft = $state<Document>(initialDraft());
	const initialCanvas = initialCanvasState();
	// Svelte Flow owns the mutable node/edge objects while the editor replaces
	// the arrays at each canonical-document boundary. Keeping them raw avoids
	// wrapping Flow internals in Svelte proxies on large canvases.
	let nodes = $state.raw<EditorFlowNode[]>(initialCanvas.nodes);
	let edges = $state.raw<EditorFlowEdge[]>(initialCanvas.edges);
	let selectedNodeID = $state<string | null>(null);
	let selectedEdgeID = $state<string | null>(null);
	let pickerOpen = $state(false);
	let triggersOnly = $state(false);
	let propertyPanelOpen = $state(false);
	let propertyDialog = $state<HTMLDivElement>();
	let propertyCloseButton = $state<HTMLButtonElement>();
	let propertyReturnFocus = $state<HTMLElement | null>(null);
	let wasPropertyPanelOpen = $state(false);

	const dirty = $derived(!workflowDocumentEquals(document, draft));
	const selectedNode = $derived((draft.nodes ?? []).find((node) => node.id === selectedNodeID) ?? null);
	const selectedDefinition = $derived(
		selectedNode ? definitions.find((definition) => definition.type === selectedNode.type && definition.version === selectedNode.typeVersion) ?? null : null
	);

	$effect(() => {
		if (propertyPanelOpen && narrow.current && !wasPropertyPanelOpen) {
			propertyReturnFocus = globalThis.document.activeElement instanceof HTMLElement ? globalThis.document.activeElement : null;
			void tick().then(() => propertyCloseButton?.focus());
		}
		wasPropertyPanelOpen = propertyPanelOpen;
	});

	$effect(() => {
		// Save validation errors arrive after the local graph has been drawn. Rebuild
		// only the Flow projection so compiler locations become visible without
		// discarding the user's canonical draft or current selection.
		const canvas = documentFromCanvas(draft, definitions, saveIssues);
		nodes = canvas.nodes.map((node) => ({ ...node, selected: node.id === selectedNodeID }));
		edges = canvas.edges.map((edge) => ({ ...edge, selected: edge.id === selectedEdgeID }));
	});

	function replaceDraft(next: Document) {
		draft = next;
		const canvas = documentFromCanvas(next, definitions, saveIssues);
		// Hydrating canvas data from the canonical draft must not discard the
		// current inspector selection after each property keystroke.
		nodes = canvas.nodes.map((node) => ({ ...node, selected: node.id === selectedNodeID }));
		edges = canvas.edges.map((edge) => ({ ...edge, selected: edge.id === selectedEdgeID }));
	}

	function initialDraft(): Document {
		return cloneWorkflowDocument(document);
	}

	function openPicker(onlyTriggers = false) {
		triggersOnly = onlyTriggers;
		pickerOpen = true;
	}

	function addNode(definition: Definition) {
		const count = draft.nodes?.length ?? 0;
		const node = createWorkflowNode(definition, nextNodePosition(count));
		selectedNodeID = node.id;
		selectedEdgeID = null;
		replaceDraft({ ...draft, nodes: [...(draft.nodes ?? []), node] });
		propertyPanelOpen = true;
	}

	function syncCanvas() {
		replaceDraft(documentFromFlow(draft, nodes, edges));
	}

	function onConnect(connection: FlowConnection) {
		const next = connectionFromCanvas(connection, draft.nodes ?? [], definitions, draft.connections ?? []);
		if (!next) return;
		replaceDraft({ ...draft, connections: [...(draft.connections ?? []), next] });
	}

	function onSelectionChange({ nodes: selectedNodes, edges: selectedEdges }: { nodes: EditorFlowNode[]; edges: EditorFlowEdge[] }) {
		selectedNodeID = selectedNodes[0]?.id ?? null;
		selectedEdgeID = selectedEdges[0]?.id ?? null;
		propertyPanelOpen = selectedNodes.length > 0;
	}

	function removeSelected() {
		if (selectedNodeID) {
			const nodeID = selectedNodeID;
			replaceDraft({
				...draft,
				nodes: (draft.nodes ?? []).filter((node) => node.id !== nodeID),
				connections: (draft.connections ?? []).filter((connection) => connection.source.nodeId !== nodeID && connection.target.nodeId !== nodeID)
			});
		} else if (selectedEdgeID) {
			replaceDraft({ ...draft, connections: (draft.connections ?? []).filter((connection) => connection.id !== selectedEdgeID) });
		}
		selectedNodeID = null;
		selectedEdgeID = null;
		propertyPanelOpen = false;
	}

	function onDelete() {
		syncCanvas();
		selectedNodeID = null;
		selectedEdgeID = null;
		propertyPanelOpen = false;
	}

	function updateProperty(scope: PropertyScope, key: string, value: unknown) {
		if (!selectedNode) return;
		replaceDraft(updateNodeProperty(draft, selectedNode.id, scope, key, value));
	}

	function focusValidationIssue(issue: CanvasValidationIssue) {
		selectedNodeID = issue.nodeID ?? null;
		selectedEdgeID = issue.connectionID ?? null;
		propertyPanelOpen = Boolean(issue.nodeID);
	}

	function closePropertyPanel() {
		propertyPanelOpen = false;
		void tick().then(() => propertyReturnFocus?.focus());
	}

	function handlePropertyDialogKeydown(event: KeyboardEvent) {
		if (event.key === 'Escape') {
			closePropertyPanel();
			return;
		}
		if (event.key !== 'Tab' || !propertyDialog) return;
		const focusable = [...propertyDialog.querySelectorAll<HTMLElement>('a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])')];
		const first = focusable[0];
		const last = focusable.at(-1);
		if (!first || !last) return;
		if (event.shiftKey && globalThis.document.activeElement === first) {
			event.preventDefault();
			last.focus();
		} else if (!event.shiftKey && globalThis.document.activeElement === last) {
			event.preventDefault();
			first.focus();
		}
	}

	async function save() {
		if (!dirty || saving) return;
		await onSave(toWorkflowInput(draft));
	}

	async function run() {
		if (dirty || running) return;
		await onRun();
	}
</script>

<section class="relative flex h-full min-h-0 flex-col bg-background" aria-label="Workflow editor">
	<header class="flex flex-wrap items-center gap-2 border-b border-border bg-card px-3 py-2 sm:px-4">
		<button type="button" class="inline-flex h-9 items-center gap-2 rounded-lg bg-primary px-3 text-sm font-medium text-primary-foreground hover:opacity-90 focus-visible:outline-2 focus-visible:outline-offset-2" onclick={() => openPicker(false)}>
			<Plus aria-hidden="true" class="size-4" />Add step
		</button>
		<button type="button" class="inline-flex h-9 items-center gap-2 rounded-lg border border-border px-3 text-sm font-medium hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-2" disabled={!dirty || saving} onclick={() => void save()}>
			<Save aria-hidden="true" class="size-4" />{saving ? 'Saving…' : 'Save'}
		</button>
		<button type="button" class="inline-flex h-9 items-center gap-2 rounded-lg border border-border px-3 text-sm font-medium hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-2 disabled:cursor-not-allowed disabled:opacity-50" disabled={dirty || running} aria-describedby={dirty ? 'save-before-run' : undefined} onclick={() => void run()}>
			<Play aria-hidden="true" class="size-4" />{running ? 'Running…' : 'Run'}
		</button>
		{#if selectedNodeID || selectedEdgeID}
			<button type="button" class="ml-auto inline-flex h-9 items-center gap-2 rounded-lg px-3 text-sm font-medium text-destructive hover:bg-destructive/10 focus-visible:outline-2 focus-visible:outline-offset-2" onclick={removeSelected}>
				<Trash2 aria-hidden="true" class="size-4" />Remove selected
			</button>
		{/if}
		<span class="basis-full text-xs text-muted-foreground sm:ml-auto sm:basis-auto">{dirty ? 'Unsaved changes' : 'All changes saved'}</span>
		{#if dirty}<span id="save-before-run" class="sr-only">Save changes before running this workflow.</span>{/if}
	</header>

	{#if saveError}
		<p role="alert" class="border-b border-destructive/25 bg-destructive/5 px-4 py-2 text-sm text-destructive">Save failed: {saveError}</p>
	{/if}
	{#if saveIssues.length > 0}
		<ul aria-label="Workflow validation issues" class="border-b border-destructive/20 bg-destructive/5 px-4 py-2 text-sm text-destructive">
			{#each saveIssues as issue (`${issue.code ?? ''}-${issue.nodeID ?? issue.connectionID ?? issue.message}`)}
				<li><button type="button" class="text-left underline decoration-destructive/40 underline-offset-2 hover:decoration-destructive" onclick={() => focusValidationIssue(issue)}>{issue.message}{#if issue.nodeID} (node){:else if issue.connectionID} (connection){/if}</button></li>
			{/each}
		</ul>
	{/if}
	{#if runError}
		<p role="alert" class="border-b border-destructive/25 bg-destructive/5 px-4 py-2 text-sm text-destructive">Run failed: {runError}</p>
	{:else if runMessage}
		<p role="status" class="border-b border-success/25 bg-success/5 px-4 py-2 text-sm text-success-foreground">{runMessage}</p>
	{/if}

	<div class="relative flex min-h-0 flex-1 flex-col lg:grid lg:grid-cols-[minmax(0,1fr)_22rem]">
		<div class="relative min-h-0 flex-1 overflow-hidden" data-testid="workflow-canvas">
			<SvelteFlow bind:nodes bind:edges {nodeTypes} fitView deleteKey={['Backspace', 'Delete']} isValidConnection={(connection) => canConnect(connection, draft.nodes ?? [], definitions, draft.connections ?? [])} onconnect={onConnect} ondelete={onDelete} onnodedragstop={syncCanvas} onselectionchange={onSelectionChange} onpaneclick={() => onSelectionChange({ nodes: [], edges: [] })}>
				<Background variant={BackgroundVariant.Dots} gap={20} size={1} patternColor="var(--border)" />
				<Controls showLock={false} />
			</SvelteFlow>

			{#if (draft.nodes?.length ?? 0) === 0}
				<div class="pointer-events-none absolute inset-0 grid place-items-center p-6">
					<div class="pointer-events-auto max-w-xs text-center">
						<button type="button" class="mx-auto grid size-16 place-items-center rounded-2xl border-2 border-dashed border-border bg-card text-muted-foreground hover:border-primary hover:text-primary focus-visible:outline-2 focus-visible:outline-offset-4" aria-label="Add first workflow step" onclick={() => openPicker(true)}><Plus aria-hidden="true" class="size-7" /></button>
						<h2 class="mt-4 text-base font-semibold">Start with a trigger</h2>
						<p class="mt-1 text-sm leading-6 text-muted-foreground">Choose a registered trigger, then add the steps it should run.</p>
					</div>
				</div>
			{/if}
		</div>

		{#if !narrow.current}
			<aside class="hidden min-h-0 border-l border-border lg:block">
				{#if selectedNode && selectedDefinition}
					<PropertiesPanel node={selectedNode} definition={selectedDefinition} onChange={updateProperty} />
				{:else}
					<div class="grid h-full place-items-center p-6 text-center text-sm leading-6 text-muted-foreground">Select a node to edit its registry-defined parameters and shared settings.</div>
				{/if}
			</aside>
		{/if}

		{#if narrow.current && propertyPanelOpen && selectedNode && selectedDefinition}
			<div bind:this={propertyDialog} class="absolute inset-x-3 bottom-3 z-30 max-h-[min(32rem,calc(100%-1.5rem))] overflow-hidden rounded-xl border border-border bg-card shadow-xl" role="dialog" aria-modal="true" aria-label={`${selectedNode.name} properties`} tabindex="-1" onkeydown={handlePropertyDialogKeydown}>
				<div class="flex justify-end border-b border-border px-2 py-1"><button bind:this={propertyCloseButton} type="button" class="rounded-md p-2 text-muted-foreground hover:bg-muted" aria-label="Close node properties" onclick={closePropertyPanel}><PanelLeftClose aria-hidden="true" class="size-4" /></button></div>
				<PropertiesPanel node={selectedNode} definition={selectedDefinition} onChange={updateProperty} />
			</div>
		{/if}
	</div>

	<NodePicker bind:open={pickerOpen} {definitions} {triggersOnly} onSelect={addNode} />
</section>
