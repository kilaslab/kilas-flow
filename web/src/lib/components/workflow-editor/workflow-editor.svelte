<script lang="ts">
	import { tick, type Snippet } from 'svelte';
	import '@xyflow/svelte/dist/style.css';

	import { Background, BackgroundVariant, Controls, SvelteFlow, type Connection as FlowConnection } from '@xyflow/svelte';
	import Play from '@lucide/svelte/icons/play';
	import Plus from '@lucide/svelte/icons/plus';
	import Save from '@lucide/svelte/icons/save';
	import Trash2 from '@lucide/svelte/icons/trash-2';
	import X from '@lucide/svelte/icons/x';

	import type { CredentialResource, Definition, Document, WorkflowDocumentInput } from '$lib/api/generated/models';
	import { setCanvasActions } from '$lib/workflow-editor/canvas-actions';
	import {
		cloneWorkflowDocument,
		createWorkflowNode,
		documentFromCanvas,
		documentFromFlow,
		nextNodePosition,
		positionAfter,
		toWorkflowInput,
		updateNodeCredential,
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
		header,
		document,
		definitions,
		credentials = [],
		readOnly = false,
		hideRun = false,
		hideSave = false,
		saving = false,
		running = false,
		saveError = null,
		saveIssues = [],
		runError = null,
		runMessage = null,
		onSave,
		onRun
	}: {
		/** Rendered at the head of the toolbar. The embed surface passes none. */
		header?: Snippet;
		document: Document;
		definitions: Definition[];
		credentials?: CredentialResource[];
		// A read-only editor still renders and still inspects; it simply cannot
		// change anything. The server enforces the same boundary, so hiding a
		// control is never the thing that stops an edit.
		readOnly?: boolean;
		hideRun?: boolean;
		hideSave?: boolean;
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
	// Set when the picker was opened from a node's output port, so the step it
	// adds arrives already connected instead of stranded on the canvas.
	let pendingSource = $state<{ nodeID: string; port: string } | null>(null);
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
	// The inspector only exists while a node is selected. A permanent empty panel
	// would cost the canvas 20rem to say nothing, and the canvas is what the user
	// came here for.
	const showInspector = $derived(!narrow.current && Boolean(selectedNode && selectedDefinition));

	setCanvasActions({
		readOnly: () => readOnly,
		addFrom: (nodeID, port) => openPickerFrom(nodeID, port),
		remove: (nodeID) => removeNode(nodeID)
	});

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
		pendingSource = null;
		triggersOnly = onlyTriggers;
		pickerOpen = true;
	}

	function openPickerFrom(nodeID: string, port: string) {
		if (readOnly) return;
		pendingSource = { nodeID, port };
		triggersOnly = false;
		pickerOpen = true;
	}

	function addNode(definition: Definition) {
		if (readOnly) return;
		const existing = draft.nodes ?? [];
		const from = pendingSource;
		const source = from ? existing.find((candidate) => candidate.id === from.nodeID) : undefined;
		// A branch fans downward: each step already leaving this port pushes the
		// next one a row further so two of them never land on top of each other.
		const taken = from ? (draft.connections ?? []).filter((edge) => edge.source.nodeId === from.nodeID && edge.source.port === from.port).length : 0;
		const node = createWorkflowNode(definition, source ? positionAfter(source.position, taken) : nextNodePosition(existing.length));

		const nextNodes = [...existing, node];
		let connections = draft.connections ?? [];
		if (from) {
			const target = (definition.inputs ?? []).find((port) => port.Kind === 'main');
			// Built through the same validator a dragged connection uses, so a step
			// added from a port can never produce an edge the canvas would refuse.
			const connection = target
				? connectionFromCanvas({ source: from.nodeID, sourceHandle: from.port, target: node.id, targetHandle: target.Name }, nextNodes, definitions, connections)
				: null;
			if (connection) connections = [...connections, connection];
		}

		pendingSource = null;
		selectedNodeID = node.id;
		selectedEdgeID = null;
		replaceDraft({ ...draft, nodes: nextNodes, connections });
		propertyPanelOpen = true;
	}

	function syncCanvas() {
		if (readOnly) return;
		replaceDraft(documentFromFlow(draft, nodes, edges));
	}

	function onConnect(connection: FlowConnection) {
		if (readOnly) return;
		const next = connectionFromCanvas(connection, draft.nodes ?? [], definitions, draft.connections ?? []);
		if (!next) return;
		replaceDraft({ ...draft, connections: [...(draft.connections ?? []), next] });
	}

	function onSelectionChange({ nodes: selectedNodes, edges: selectedEdges }: { nodes: EditorFlowNode[]; edges: EditorFlowEdge[] }) {
		selectedNodeID = selectedNodes[0]?.id ?? null;
		selectedEdgeID = selectedEdges[0]?.id ?? null;
		propertyPanelOpen = selectedNodes.length > 0;
	}

	function removeNode(nodeID: string) {
		if (readOnly) return;
		replaceDraft({
			...draft,
			nodes: (draft.nodes ?? []).filter((node) => node.id !== nodeID),
			connections: (draft.connections ?? []).filter((connection) => connection.source.nodeId !== nodeID && connection.target.nodeId !== nodeID)
		});
		if (selectedNodeID === nodeID) {
			selectedNodeID = null;
			propertyPanelOpen = false;
		}
	}

	function removeSelected() {
		if (readOnly) return;
		if (selectedNodeID) {
			removeNode(selectedNodeID);
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
		if (readOnly || !selectedNode) return;
		replaceDraft(updateNodeProperty(draft, selectedNode.id, scope, key, value));
	}

	function updateCredential(typeID: string, credentialID: string) {
		if (readOnly || !selectedNode) return;
		replaceDraft(updateNodeCredential(draft, selectedNode.id, typeID, credentialID));
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
		if (readOnly || !dirty || saving) return;
		await onSave(toWorkflowInput(draft));
	}

	async function run() {
		if (hideRun || dirty || running) return;
		await onRun();
	}
</script>

<section class="relative flex h-full min-h-0 flex-col bg-background" aria-label="Workflow editor">
	<header class="flex h-10 shrink-0 items-center gap-1.5 overflow-x-auto border-b border-border bg-card px-2">
		{#if header}
			{@render header()}
			<span aria-hidden="true" class="mx-1 h-4 w-px bg-border"></span>
		{/if}
		{#if !readOnly}
			<button type="button" class="inline-flex h-7 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-md bg-primary px-2.5 text-xs font-medium text-primary-foreground transition-opacity hover:opacity-90 focus-visible:outline-2 focus-visible:outline-offset-2" onclick={() => openPicker(false)}>
				<Plus aria-hidden="true" class="size-3.5" />Add step
			</button>
		{/if}
		{#if !readOnly && !hideSave}
			<button type="button" class="inline-flex h-7 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-md border border-border px-2.5 text-xs font-medium transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-2 disabled:opacity-40" disabled={!dirty || saving} onclick={() => void save()}>
				<Save aria-hidden="true" class="size-3.5" />{saving ? 'Saving…' : 'Save'}
			</button>
		{/if}
		{#if !hideRun}
			<button type="button" class="inline-flex h-7 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-md border border-border px-2.5 text-xs font-medium transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-2 disabled:cursor-not-allowed disabled:opacity-40" disabled={dirty || running} aria-describedby={dirty ? 'save-before-run' : undefined} onclick={() => void run()}>
				<Play aria-hidden="true" class="size-3.5" />{running ? 'Running…' : 'Run'}
			</button>
		{/if}
		{#if !readOnly && selectedEdgeID && !selectedNodeID}
			<button type="button" class="inline-flex h-7 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-md px-2.5 text-xs font-medium text-destructive transition-colors hover:bg-destructive/10 focus-visible:outline-2 focus-visible:outline-offset-2" onclick={removeSelected}>
				<Trash2 aria-hidden="true" class="size-3.5" />Delete connection
			</button>
		{/if}
		<span class="ml-auto flex shrink-0 items-center gap-1.5 pr-1 text-[0.6875rem] text-muted-foreground" aria-live="polite">
			{#if dirty && !readOnly}<span aria-hidden="true" class="size-1.5 rounded-full bg-warning"></span>{/if}
			<span class="hidden whitespace-nowrap sm:inline">{readOnly ? 'Read only' : dirty ? 'Unsaved changes' : 'All changes saved'}</span>
			<span class="sr-only sm:hidden">{readOnly ? 'Read only' : dirty ? 'Unsaved changes' : 'All changes saved'}</span>
		</span>
		{#if dirty}<span id="save-before-run" class="sr-only">Save changes before running this workflow.</span>{/if}
	</header>

	{#if saveError}
		<p role="alert" class="shrink-0 border-b border-destructive/25 bg-destructive/5 px-3 py-1.5 text-xs text-destructive">Save failed: {saveError}</p>
	{/if}
	{#if saveIssues.length > 0}
		<ul aria-label="Workflow validation issues" class="shrink-0 divide-y divide-destructive/10 border-b border-destructive/20 bg-destructive/5">
			{#each saveIssues as issue (`${issue.code ?? ''}-${issue.nodeID ?? issue.connectionID ?? issue.message}`)}
				<li><button type="button" class="w-full px-3 py-1.5 text-left text-xs text-destructive underline decoration-destructive/30 underline-offset-2 hover:decoration-destructive" onclick={() => focusValidationIssue(issue)}>{issue.message}{#if issue.nodeID} (node){:else if issue.connectionID} (connection){/if}</button></li>
			{/each}
		</ul>
	{/if}
	{#if runError}
		<p role="alert" class="shrink-0 border-b border-destructive/25 bg-destructive/5 px-3 py-1.5 text-xs text-destructive">Run failed: {runError}</p>
	{:else if runMessage}
		<p role="status" class="shrink-0 border-b border-success/25 bg-success/5 px-3 py-1.5 text-xs text-success">{runMessage}</p>
	{/if}

	<div class="relative flex min-h-0 flex-1 flex-col lg:grid" style={showInspector ? 'grid-template-columns: minmax(0,1fr) 20rem' : 'grid-template-columns: minmax(0,1fr)'}>
		<div class="relative min-h-0 flex-1 overflow-hidden" data-testid="workflow-canvas">
			<SvelteFlow bind:nodes bind:edges {nodeTypes} fitView fitViewOptions={{ padding: 0.15, maxZoom: 1 }} minZoom={0.3} nodesDraggable={!readOnly} nodesConnectable={!readOnly} deleteKey={readOnly ? null : ['Backspace', 'Delete']} isValidConnection={(connection) => canConnect(connection, draft.nodes ?? [], definitions, draft.connections ?? [])} onconnect={onConnect} ondelete={onDelete} onnodedragstop={syncCanvas} onselectionchange={onSelectionChange} onpaneclick={() => onSelectionChange({ nodes: [], edges: [] })}>
				<Background variant={BackgroundVariant.Dots} gap={16} size={1} patternColor="var(--border)" />
				<Controls showLock={false} />
			</SvelteFlow>

			{#if (draft.nodes?.length ?? 0) === 0 && !readOnly}
				<div class="pointer-events-none absolute inset-0 grid place-items-center p-4">
					<div class="pointer-events-auto text-center">
						<button type="button" class="mx-auto grid h-22 w-22 place-items-center rounded-l-[2.75rem] rounded-r-xl border border-dashed border-border bg-card text-muted-foreground transition-colors hover:border-primary hover:text-primary focus-visible:outline-2 focus-visible:outline-offset-4" aria-label="Add first workflow step" onclick={() => openPicker(true)}>
							<Plus aria-hidden="true" class="size-6" />
						</button>
						<h2 class="mt-3 text-sm font-semibold">Start with a trigger</h2>
						<p class="mt-1 max-w-56 text-xs leading-5 text-muted-foreground">Pick what starts this workflow, then add the steps it runs.</p>
					</div>
				</div>
			{/if}
		</div>

		{#if showInspector && selectedNode && selectedDefinition}
			<aside class="hidden min-h-0 border-l border-border lg:block">
				<PropertiesPanel node={selectedNode} definition={selectedDefinition} {credentials} {readOnly} onChange={updateProperty} onCredentialChange={updateCredential} />
			</aside>
		{/if}

		{#if narrow.current && propertyPanelOpen && selectedNode && selectedDefinition}
			<div bind:this={propertyDialog} class="absolute inset-x-2 bottom-2 z-30 max-h-[min(28rem,calc(100%-1rem))] overflow-hidden rounded-xl border border-border bg-card shadow-xl" role="dialog" aria-modal="true" aria-label={`${selectedNode.name} properties`} tabindex="-1" onkeydown={handlePropertyDialogKeydown}>
				<div class="flex justify-end border-b border-border px-1.5 py-1"><button bind:this={propertyCloseButton} type="button" class="grid size-6 place-items-center rounded-md text-muted-foreground hover:bg-muted" aria-label="Close node properties" onclick={closePropertyPanel}><X aria-hidden="true" class="size-3.5" /></button></div>
				<PropertiesPanel node={selectedNode} definition={selectedDefinition} {credentials} {readOnly} onChange={updateProperty} onCredentialChange={updateCredential} />
			</div>
		{/if}
	</div>

	<NodePicker bind:open={pickerOpen} {definitions} {triggersOnly} connecting={Boolean(pendingSource)} onSelect={addNode} onDismiss={() => (pendingSource = null)} />
</section>
