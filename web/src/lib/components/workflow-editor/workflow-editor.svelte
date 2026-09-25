<script lang="ts" module>
	import type { WorkflowResource, WorkflowVersionSummaryResource } from '$lib/api/generated/models';
	import type { ExecutionEvent } from '$lib/workflow-editor/event-stream.svelte';

	/**
	 * Everything the version panel needs, as one prop.
	 *
	 * Bundled rather than spread across eight props so the dashboard page and
	 * the embed shell cannot mount the panel with different halves of it wired
	 * up — the two hosts have drifted on error handling once already.
	 */
	/**
	 * What a manual run needs to know beyond the workflow's own id.
	 *
	 * A workflow can declare several triggers, and firing all of them is rarely
	 * what Execute means — for a webhook trigger it is a run with an empty item.
	 */
	export type RunSelection = {
		/** The trigger to start from; omitted runs every trigger, as before. */
		triggerNodeId?: string;
		/** Item the named trigger emits. The Chat panel sends `{action, sessionId, chatInput}`. */
		input?: unknown;
		/**
		 * Live events of the queued run, for a host that can stream them. The
		 * Chat panel uses them to show the reply being written and the tools
		 * being called; a host that cannot stream simply never calls it.
		 */
		onEvent?: (event: ExecutionEvent) => void;
	};

	export type WorkflowHistoryHost = {
		workflowID: string;
		/** The revision the canvas was loaded from. */
		latestVersionID: string;
		canRestore: boolean;
		canPublish: boolean;
		onRestored: (workflow: WorkflowResource) => void;
		onPublished: (workflow: WorkflowResource, version: WorkflowVersionSummaryResource) => void;
		onUnpublished: (workflow: WorkflowResource) => void;
	};
</script>

<script lang="ts">
	import { tick, type Snippet } from 'svelte';

	import { Background, BackgroundVariant, MiniMap, SvelteFlow, ViewportPortal, type Connection as FlowConnection, type EdgeTypes } from '@xyflow/svelte';
	import History from '@lucide/svelte/icons/history';
	import Keyboard from '@lucide/svelte/icons/keyboard';
	import Redo2 from '@lucide/svelte/icons/redo-2';
	import Undo2 from '@lucide/svelte/icons/undo-2';
	import Activity from '@lucide/svelte/icons/activity';
	import MessageCircle from '@lucide/svelte/icons/message-circle';
	import Play from '@lucide/svelte/icons/play';
	import Plus from '@lucide/svelte/icons/plus';
	import Power from '@lucide/svelte/icons/power';
	import PowerOff from '@lucide/svelte/icons/power-off';
	import Save from '@lucide/svelte/icons/save';
	import Trash2 from '@lucide/svelte/icons/trash-2';
	import WandSparkles from '@lucide/svelte/icons/wand-sparkles';
	import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
	import X from '@lucide/svelte/icons/x';

	import type { CredentialResource, Definition, Document, ExecutionResource, Node as WorkflowNode, WorkflowDocumentInput } from '$lib/api/generated/models';
	import * as m from '$lib/paraglide/messages.js';
	import type { ActivationNoticeView } from '$lib/workflow-editor/activation';
	import { setCanvasActions } from '$lib/workflow-editor/canvas-actions';
	import {
		cloneWorkflowDocument,
		createWorkflowNode,
		documentFromCanvas,
		documentFromFlow,
		duplicateNodes,
		nextNodePosition,
		positionAfter,
		renameNode,
		resolveDefinition,
		toWorkflowInput,
		uniqueNodeName,
		updateNodeCredential,
		updateNodeProperty,
		updateNodeSize,
		workflowDocumentEquals,
		type EditorFlowEdge,
		type EditorFlowNode,
		type PropertyScope
	} from '$lib/workflow-editor/document';
	import { copySelection, pasteInto, readClipboard } from '$lib/workflow-editor/clipboard';
	import { emptyHistory, record as recordHistory, redo as redoHistory, undo as undoHistory, type History as DocumentHistory } from '$lib/workflow-editor/history';
	import { SHORTCUT_REFERENCE, CANVAS_DELETE_KEYS, canvasShortcut, controlOwnsKey, isTypingTarget } from '$lib/workflow-editor/shortcuts';
	import { tidyDocument } from '$lib/workflow-editor/layout';
	import { mediaQuery } from '$lib/workflow-editor/media.svelte';
	import { isAnnotation } from '$lib/workflow-editor/node-visual';
	import { chatHasMemory, chatTriggerIn, type ChatSendPayload } from '$lib/workflow-editor/chat';
	import { executeIntent } from '$lib/workflow-editor/run-trigger';
	import { canConnect, connectionFromCanvas, resolvedPorts } from '$lib/workflow-editor/ports';
	import type { CanvasShortcut } from '$lib/workflow-editor/shortcuts';
	import type { CanvasValidationIssue } from '$lib/workflow-editor/validation';

	import ActivationNotices from './activation-notices.svelte';
	import CanvasChatPanel from './canvas-chat-panel.svelte';
	import EditorControls from './editor-controls.svelte';
	import CanvasBridge, { type CanvasFlow } from './canvas-bridge.svelte';
	import CanvasEdge from './canvas-edge.svelte';
	import CanvasNode from './canvas-node.svelte';
	import NodePicker from './node-picker.svelte';
	import PropertiesPanel from './properties-panel.svelte';
	import VersionPanel from './version-panel.svelte';

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
		lastExecutionId = null,
		active = false,
		activating = false,
		notices = [],
		activationError = null,
		history = null,
		saveConflict = false,
		hostIssues = [],
		onSave,
		onRun,
		onActivate,
		onDeactivate,
		onDismissNotice,
		onDirtyChange,
		onReloadConflict,
		onOverwriteConflict
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
		/** Most recent execution started from this editor session, for deep-link feedback. */
		lastExecutionId?: string | null;
		/** Whether the server currently has this workflow pinned active. */
		active?: boolean;
		activating?: boolean;
		/** What the last activation could not do for the user, until dismissed. */
		notices?: ActivationNoticeView[];
		activationError?: string | null;
		/** Supplied by a surface that can reach the version history. Null hides it. */
		history?: WorkflowHistoryHost | null;
		/** The server holds a newer revision than the one this draft was loaded from. */
		saveConflict?: boolean;
		/** Compile failures a host learned from activate/run, shown beside the save issues. */
		hostIssues?: CanvasValidationIssue[];
		onSave: (input: WorkflowDocumentInput) => Promise<void>;
		/**
		 * Queues a manual run of the saved revision.
		 *
		 * The options name the trigger to start from, when the workflow declares
		 * several and the user picked one; a host that ignores them keeps the
		 * server's "run every trigger" default. Chat sends the trigger plus the
		 * message payload and uses the returned execution for the reply.
		 */
		onRun: (selection?: RunSelection) => Promise<ExecutionResource | void>;
		onActivate?: () => Promise<void>;
		onDeactivate?: () => Promise<void>;
		onDismissNotice?: (key: string) => void;
		/**
		 * Whether the canvas has edits the server has not stored.
		 *
		 * The host owns the browser-level guards — leaving the page, adopting a
		 * newer server revision — and only this component knows the answer.
		 */
		onDirtyChange?: (dirty: boolean) => void;
		/** Replace the draft with the server's newer revision. */
		onReloadConflict?: () => void;
		/** Keep this draft and store it over the newer revision. */
		onOverwriteConflict?: () => void;
	} = $props();

	const nodeTypes = { workflow: CanvasNode };
	// Registered under the built-in names so the replay canvas, which uses the
	// same projection and registers nothing, keeps rendering plain edges.
	// Typed as the library's own edge map: the component receives the built-in
	// edge props plus `data`, which the canvas edge ignores.
	const edgeTypes = { smoothstep: CanvasEdge, default: CanvasEdge } as unknown as EdgeTypes;
	// Matches the `lg:` breakpoint the layout below switches on.
	const narrow = mediaQuery('(max-width: 1023.98px)');
	function initialCanvasState() {
		return documentFromCanvas(document, definitions);
	}
	// `raw` state on purpose: assigning a proxied document deep-wraps every node
	// and parameter on each keystroke, which is work the canvas never reads —
	// everything that touches the draft replaces it wholesale.
	let draft = $state.raw<Document>(initialDraft());
	const initialCanvas = initialCanvasState();
	// Svelte Flow owns the mutable node/edge objects while the editor replaces
	// the arrays at each canonical-document boundary. Keeping them raw avoids
	// wrapping Flow internals in Svelte proxies on large canvases.
	let nodes = $state.raw<EditorFlowNode[]>(initialCanvas.nodes);
	let edges = $state.raw<EditorFlowEdge[]>(initialCanvas.edges);
	/**
	 * Undo/redo, as snapshots of the whole draft.
	 *
	 * A draft is already replaced rather than mutated, so keeping the previous
	 * one costs a reference — while an inverse operation per edit would be a
	 * second implementation of every mutation, free to drift from the first.
	 */
	let undoStack = $state.raw<DocumentHistory<Document>>(emptyHistory());
	const canUndo = $derived(undoStack.past.length > 0);
	const canRedo = $derived(undoStack.future.length > 0);
	/** Every selected node, not just the first: a group drag used to lose all but one. */
	let selectedNodeIDs = $state<string[]>([]);
	let selectedNodeID = $state<string | null>(null);
	let selectedEdgeID = $state<string | null>(null);
	let pickerOpen = $state(false);
	let triggersOnly = $state(false);
	// Set when the picker was opened from a node's output port, so the step it
	// adds arrives already connected instead of stranded on the canvas.
	let pendingSource = $state<{ nodeID: string; port: string } | null>(null);
	/** The connection kind an attachment slot accepts, so the picker offers only what fits. */
	let pendingKind = $state<string | null>(null);
	/** The connection a new step is being spliced into, or null. */
	let pendingSplice = $state<string | null>(null);
	/** Where a wire was dropped on empty canvas: the new step lands under the pointer. */
	let pendingPosition = $state<{ x: number; y: number } | null>(null);
	/** The node whose name is being edited inline, and the text so far. */
	let renameTarget = $state<string | null>(null);
	let renameValue = $state('');
	let renameInput = $state<HTMLInputElement>();
	let shortcutsOpen = $state(false);
	/** What the last clipboard action did, until the next action. */
	let canvasMessage = $state<string | null>(null);
	let editorSection = $state<HTMLElement>();
	let flow = $state<CanvasFlow | null>(null);

	/**
	 * SvelteFlow's own control chrome, in the runtime locale.
	 *
	 * The zoom buttons carry these as both their accessible name and their
	 * tooltip, so they are copy a sighted user reads — the library ships them in
	 * English and reads the override out of this prop. Derived rather than
	 * declared, so flipping the locale re-labels the buttons without a remount.
	 */
	const ariaLabels = $derived({
		'controls.ariaLabel': m.canvas_controls_aria(),
		'controls.zoomIn.ariaLabel': m.canvas_zoom_in(),
		'controls.zoomOut.ariaLabel': m.canvas_zoom_out(),
		'controls.fitView.ariaLabel': m.canvas_fit_view()
	});
	let propertyPanelOpen = $state(false);
	let propertyDialog = $state<HTMLDivElement>();
	let propertyCloseButton = $state<HTMLButtonElement>();
	let propertyReturnFocus = $state<HTMLElement | null>(null);
	let wasPropertyPanelOpen = $state(false);
	let canvasRegion = $state<HTMLDivElement>();
	let inspectorRegion = $state<HTMLElement>();
	let historyOpen = $state(false);
	/**
	 * The stored revision being previewed on the canvas, or null for the draft.
	 *
	 * The preview is drawn *beside* `draft` rather than into it. Loading a
	 * version into the draft and putting it back afterwards would be the same
	 * work with one extra failure — anything that went wrong mid-way, including
	 * closing the panel, would leave the user's unsaved edits overwritten by a
	 * version they were only looking at.
	 */
	let preview = $state.raw<{ versionID: string; revision: number; document: Document } | null>(null);
	/** Compile failures a publish was refused with, shown in the save-issue list. */
	let historyIssues = $state<CanvasValidationIssue[]>([]);

	const previewing = $derived(preview !== null);
	/** What the canvas is showing: the previewed revision, or the draft. */
	const displayed = $derived(preview?.document ?? draft);
	// Previewing is read-only for the same reason `readOnly` is, and is folded
	// into one flag so a new mutation cannot be added that honours one and not
	// the other.
	const locked = $derived(readOnly || previewing);
	const issues = $derived([...saveIssues, ...hostIssues, ...historyIssues]);

	const dirty = $derived(!workflowDocumentEquals(document, draft));
	// The host owns the unsaved-changes guards and cannot see the draft.
	$effect(() => {
		onDirtyChange?.(dirty);
	});
	// What the toolbar's live region announces. A preview outranks the other
	// three because it is the only one under which the controls do nothing.
	const canvasStatus = $derived(
		preview
			? m.editor_state_previewing({ revision: preview.revision })
			: readOnly
				? m.editor_state_read_only()
				: dirty
					? m.editor_state_unsaved()
					: m.editor_state_saved()
	);
	// Activation appears only for a surface that supplied both halves of it. The
	// embed supplies neither, because a host application decides when its own
	// workflows go live and a button inside its iframe would take that decision
	// away from it.
	const canActivate = $derived(!locked && Boolean(onActivate) && Boolean(onDeactivate));
	// A dirty canvas has to be saved first, because activation pins the latest
	// *saved* revision: activating here would publish something other than what
	// the user is looking at. Deactivating is never ambiguous that way.
	const activationBlockedByDirty = $derived(!active && dirty);
	const chatTrigger = $derived(chatTriggerIn(displayed.nodes ?? []));
	const showChat = $derived(Boolean(chatTrigger) && !hideRun && !previewing);
	let chatOpen = $state(false);
	$effect(() => {
		if (!showChat) chatOpen = false;
	});
	const selectedNode = $derived((displayed.nodes ?? []).find((node) => node.id === selectedNodeID) ?? null);
	const selectedDefinition = $derived(
		selectedNode ? (resolveDefinition(selectedNode.type, selectedNode.typeVersion, definitions) ?? null) : null
	);
	const selectedResolvedVersion = $derived(selectedDefinition ? selectedDefinition.version : null);
	// The inspector only exists while a node is selected. A permanent empty panel
	// would cost the canvas 20rem to say nothing, and the canvas is what the user
	// came here for.
	// A selected node the catalogue does not know — most often an n8n
	// import's unsupported placeholder — has no definition, so the inspector
	// above never opens for it. It still needs a surface that says what it
	// is: the import capsule in its parameters carries the original n8n type
	// and version, and without that the placeholder is a grey tile with no
	// explanation. Read-only by construction: there is nothing to edit.
	const selectedUncatalogued = $derived(selectedNode !== null && selectedDefinition === null);
	const capsuleOrigin = $derived(
		selectedNode &&
		typeof selectedNode.parameters?.originalType === 'string' &&
		selectedNode.parameters.originalType !== ''
			? {
					type: selectedNode.parameters.originalType,
					version: selectedNode.parameters.originalTypeVersion
				}
			: null
	);
	const showInspector = $derived(!narrow.current && Boolean(selectedNode && selectedDefinition));
	// A surface that owns the keyboard while it is open. The canvas shortcuts
	// must not act behind a dialog, and on a narrow screen the inspector *is* a
	// modal.
	const overlayOpen = $derived(shortcutsOpen || pickerOpen || historyOpen || (narrow.current && propertyPanelOpen));

	setCanvasActions({
		readOnly: () => locked,
		addFrom: (nodeID, port) => openPickerFrom(nodeID, port, null),
		addAttached: (nodeID, port, kind) => openPickerFrom(nodeID, port, kind),
		remove: (nodeID) => removeNode(nodeID),
		splice: (edgeID) => openPickerSplice(edgeID),
		removeEdge: (edgeID) => removeConnection(edgeID),
		rename: (nodeID) => startRename(nodeID),
		resize: (nodeID, width, height) => {
			if (locked) return;
			replaceDraft(updateNodeSize(draft, nodeID, width, height));
		}
	});

	$effect(() => {
		if (propertyPanelOpen && narrow.current && !wasPropertyPanelOpen) {
			propertyReturnFocus = globalThis.document.activeElement instanceof HTMLElement ? globalThis.document.activeElement : null;
			void tick().then(() => propertyCloseButton?.focus());
		}
		wasPropertyPanelOpen = propertyPanelOpen;
	});

	/**
	 * The one place the canonical document becomes canvas data.
	 *
	 * Every mutation used to run this twice — once while replacing the draft and
	 * once here — which is what made typing cost a full rebuild per keystroke.
	 * The document is the input; this is its only projection.
	 */
	$effect(() => {
		const canvas = documentFromCanvas(displayed, definitions, issues);
		const selectedNow = new Set(selectedNodeIDs);
		nodes = canvas.nodes.map((node) => ({ ...node, selected: selectedNow.has(node.id) }));
		edges = canvas.edges.map((edge) => ({ ...edge, selected: edge.id === selectedEdgeID }));
	});

	/**
	 * Puts focus somewhere that still exists after a mutation.
	 *
	 * Adding and deleting both destroy the control that started them — the `+`
	 * stub disappears once its port is connected, and a delete unmounts the
	 * toolbar and the inspector. Restoring focus to a detached element silently
	 * drops the caret on `<body>`, so every mutation names a survivor instead.
	 */
	function restoreFocus(preferred?: HTMLElement | null) {
		void tick().then(() => {
			if (preferred?.isConnected) {
				preferred.focus();
				return;
			}
			canvasRegion?.focus();
		});
	}

	/**
	 * Swaps in a new draft and records the one it replaced.
	 *
	 * `key` names the control being edited when the change is one step of a run
	 * of them — typing in a field — so undo lands on the state before the first
	 * keystroke instead of the state before the last. Everything else is its own
	 * step.
	 */
	function replaceDraft(next: Document, options: { key?: string } = {}) {
		if (next === draft) return;
		undoStack = recordHistory(undoStack, draft, { key: options.key ?? null });
		draft = next;
	}

	function initialDraft(): Document {
		return cloneWorkflowDocument(document);
	}

	function openPicker(onlyTriggers = false) {
		pendingSource = null;
		pendingKind = null;
		pendingSplice = null;
		pendingPosition = null;
		triggersOnly = onlyTriggers;
		pickerOpen = true;
	}

	function openPickerFrom(nodeID: string, port: string, kind: string | null) {
		if (locked) return;
		pendingSource = { nodeID, port };
		pendingKind = kind;
		pendingSplice = null;
		pendingPosition = null;
		triggersOnly = false;
		pickerOpen = true;
	}

	/** Adds a step between the two nodes a connection already joins. */
	function openPickerSplice(edgeID: string) {
		if (locked) return;
		const connection = (draft.connections ?? []).find((candidate) => candidate.id === edgeID);
		if (!connection) return;
		pendingSplice = edgeID;
		pendingSource = { nodeID: connection.source.nodeId, port: connection.source.port };
		pendingKind = null;
		pendingPosition = null;
		triggersOnly = false;
		pickerOpen = true;
	}

	/**
	 * A wire dropped on empty canvas opens the picker.
	 *
	 * Releasing a connection in mid-air is how an n8n user chains the next step;
	 * it used to do nothing at all, so the wire had to be dragged onto a handle
	 * that did not exist yet.
	 */
	function onConnectEnd(event: MouseEvent | TouchEvent, connectionState: { toNode?: unknown }) {
		if (locked || !flow) return;
		// Ending on a handle is a connection that already exists; only a release
		// in empty space has nothing to land on and needs a node created for it.
		if (connectionState.toNode) return;
		const point = 'changedTouches' in event ? event.changedTouches[0] : event;
		if (!point) return;
		const source = connectSource;
		connectSource = null;
		if (!source) return;
		const position = flow.screenToFlowPosition({ x: point.clientX, y: point.clientY });
		pendingSource = source;
		pendingKind = null;
		pendingSplice = null;
		pendingPosition = position;
		triggersOnly = false;
		pickerOpen = true;
	}

	function addNode(definition: Definition) {
		if (locked) return;
		const existing = draft.nodes ?? [];
		const from = pendingSource;
		const source = from ? existing.find((candidate) => candidate.id === from.nodeID) : undefined;
		const dropped = pendingPosition;
		const node = createWorkflowNode(
			definition,
			dropped
				? { x: Math.round(dropped.x - TILE_CENTRE), y: Math.round(dropped.y - TILE_CENTRE) }
				: source
					? positionAfter(source.position, existing.map((candidate) => candidate.position))
					: viewportPlacement(existing.length),
			undefined,
			existing.map((candidate) => candidate.name)
		);

		const nextNodes = [...existing, node];
		const ports = resolvedPorts(node, definition);
		let connections = draft.connections ?? [];
		// Splicing keeps the wire it interrupts: the graph becomes
		// source → new step → the node the wire used to reach.
		const interrupted = pendingSplice ? connections.find((connection) => connection.id === pendingSplice) : undefined;
		if (interrupted) connections = connections.filter((connection) => connection.id !== interrupted.id);
		if (from) {
			const target = ports.inputs.find((port) => port.kind === (pendingKind ?? 'main'));
			// Built through the same validator a dragged connection uses, so a step
			// added from a port can never produce an edge the canvas would refuse.
			const connection = target
				? connectionFromCanvas({ source: from.nodeID, sourceHandle: from.port, target: node.id, targetHandle: target.name }, nextNodes, definitions, connections)
				: null;
			if (connection) connections = [...connections, connection];
		}
		if (interrupted) {
			const output = ports.outputs.find((port) => port.kind === interrupted.kind);
			const connection = output
				? connectionFromCanvas(
						{ source: node.id, sourceHandle: output.name, target: interrupted.target.nodeId, targetHandle: interrupted.target.port },
						nextNodes,
						definitions,
						connections
					)
				: null;
			if (connection) connections = [...connections, connection];
		}

		pendingSource = null;
		pendingKind = null;
		pendingSplice = null;
		pendingPosition = null;
		selectedNodeIDs = [node.id];
		selectedNodeID = node.id;
		selectedEdgeID = null;
		replaceDraft({ ...draft, nodes: nextNodes, connections });
		propertyPanelOpen = true;
		// The inspector for the new node is the natural next stop, and on a wide
		// screen nothing else moves focus there.
		restoreFocus(inspectorRegion);
		// Pan only when the node did not land in front of the user: a step placed
		// by hand on the canvas is already where they are looking.
		if (!dropped && !source) void flow?.setCenter(node.position.x + TILE_CENTRE, node.position.y + TILE_CENTRE, { duration: 250 });
	}

	function syncCanvas() {
		if (locked) return;
		replaceDraft(documentFromFlow(draft, nodes, edges));
	}

	/** Where a drag out of a handle started, for the drop-on-empty-canvas path. */
	let connectSource: { nodeID: string; port: string } | null = null;

	function onConnectStart(_event: unknown, params: { nodeId: string | null; handleId: string | null; handleType?: string | null }) {
		connectSource = params.nodeId && params.handleId ? { nodeID: params.nodeId, port: params.handleId } : null;
	}

	function onConnect(connection: FlowConnection) {
		if (locked) return;
		const next = connectionFromCanvas(connection, draft.nodes ?? [], definitions, draft.connections ?? []);
		if (!next) return;
		replaceDraft({ ...draft, connections: [...(draft.connections ?? []), next] });
	}

	/**
	 * Whether two selections name the same nodes.
	 *
	 * Order-insensitive: Flow is free to report the same selection in another
	 * order, and treating that as a new selection is what closed the loop this
	 * comparison exists to break.
	 */
	function sameSelection(left: readonly string[], right: readonly string[]): boolean {
		if (left.length !== right.length) return false;
		const held = new Set(left);
		return right.every((id) => held.has(id));
	}

	/**
	 * Adopts the selection the canvas reported.
	 *
	 * Flow answers a replaced `nodes` array with a selection event of its own,
	 * so assigning a fresh array on every event made the projection effect
	 * re-run on the output of its previous run: projection → selection event →
	 * fresh array → projection. That flush never drained, so every write queued
	 * behind it — the import report among them — never reached the DOM, and
	 * Svelte aborted the page with `effect_update_depth_exceeded` (BUG-j7rtv3).
	 * A selection equal to the one already held is therefore not written at
	 * all: the state is replaced only when what it holds really changed.
	 */
	function onSelectionChange({ nodes: selectedNodes, edges: selectedEdges }: { nodes: EditorFlowNode[]; edges: EditorFlowEdge[] }) {
		// Every selected node is kept, not just the first: collapsing a group
		// drag to one node made a following Delete remove only that one.
		const ids = selectedNodes.map((node) => node.id);
		// A selection this editor set itself reaches the canvas one projection
		// later, and Svelte Flow may report the array from before it in between.
		// Adopting that stale report is what made clicking an issue select its
		// node and immediately deselect it again.
		if (pendingSelection) {
			if (!sameSelection(ids, pendingSelection)) return;
			pendingSelection = null;
		}
		if (!sameSelection(ids, selectedNodeIDs)) selectedNodeIDs = ids;
		// The primary selection is the held array's first, not the reported
		// one's: while the set is unchanged, the held order is what the state
		// above still describes.
		selectedNodeID = selectedNodeIDs[0] ?? null;
		selectedEdgeID = selectedEdges[0]?.id ?? null;
		propertyPanelOpen = selectedNodes.length > 0;
	}

	function selectAll() {
		selectedNodeIDs = (displayed.nodes ?? []).map((node) => node.id);
		selectedNodeID = selectedNodeIDs[0] ?? null;
		selectedEdgeID = null;
		propertyPanelOpen = selectedNodeIDs.length > 0;
	}

	/** Drops ids that no longer exist, after an undo or a delete. */
	function forgetMissingSelection() {
		const present = new Set((draft.nodes ?? []).map((node) => node.id));
		const kept = selectedNodeIDs.filter((id) => present.has(id));
		if (kept.length !== selectedNodeIDs.length) {
			selectedNodeIDs = kept;
			selectedNodeID = kept[0] ?? null;
			propertyPanelOpen = kept.length > 0;
		}
	}

	function removeNode(nodeID: string) {
		if (locked) return;
		replaceDraft({
			...draft,
			nodes: (draft.nodes ?? []).filter((node) => node.id !== nodeID),
			connections: (draft.connections ?? []).filter((connection) => connection.source.nodeId !== nodeID && connection.target.nodeId !== nodeID)
		});
		if (selectedNodeID === nodeID) {
			selectedNodeID = null;
			selectedNodeIDs = selectedNodeIDs.filter((id) => id !== nodeID);
			propertyPanelOpen = false;
		}
		restoreFocus();
	}

	/** Only reachable for a connection: a node is deleted from its own toolbar. */
	function removeSelectedConnection() {
		if (locked || !selectedEdgeID) return;
		const edgeID = selectedEdgeID;
		selectedEdgeID = null;
		removeConnection(edgeID);
	}

	function removeConnection(edgeID: string) {
		if (locked) return;
		replaceDraft({ ...draft, connections: (draft.connections ?? []).filter((connection) => connection.id !== edgeID) });
		restoreFocus();
	}

	function onDelete() {
		syncCanvas();
		selectedNodeID = null;
		selectedNodeIDs = [];
		selectedEdgeID = null;
		propertyPanelOpen = false;
		restoreFocus();
	}

	function updateProperty(scope: PropertyScope, key: string, value: unknown) {
		if (locked || !selectedNode) return;
		// One history step per field, however many keystrokes it took: undoing a
		// word should not need one press per letter.
		replaceDraft(updateNodeProperty(draft, selectedNode.id, scope, key, value), {
			key: `${selectedNode.id}:${scope}:${key}`
		});
	}

	function updateCredential(typeID: string, credentialID: string) {
		if (locked || !selectedNode) return;
		replaceDraft(updateNodeCredential(draft, selectedNode.id, typeID, credentialID));
	}

	/**
	 * The selection the editor asked for and the canvas has not confirmed yet.
	 * Plain, not $state: it guards a callback and must not become a dependency.
	 * It lapses on its own after a moment so a canvas that never re-reports can
	 * never swallow the user's next click.
	 */
	let pendingSelection: string[] | null = null;

	function selectFromEditor(ids: string[]) {
		pendingSelection = ids;
		setTimeout(() => {
			if (pendingSelection === ids) pendingSelection = null;
		}, 250);
	}

	function focusValidationIssue(issue: CanvasValidationIssue) {
		selectFromEditor(issue.nodeID ? [issue.nodeID] : []);
		selectedNodeIDs = issue.nodeID ? [issue.nodeID] : [];
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


	async function tidyUp() {
		if (locked) return;
		// Three things dagre cannot know: how big the tiles actually are, which
		// nodes are annotations rather than steps, and which wires carry
		// configuration. Sizes come from Svelte Flow's own measurement, so a
		// 240px hub is laid out as a 240px hub.
		const sizes: Record<string, { width: number; height: number }> = {};
		const annotations: string[] = [];
		for (const node of draft.nodes ?? []) {
			const measured = flow?.getInternalNode(node.id)?.measured;
			if (measured?.width && measured?.height) sizes[node.id] = { width: measured.width, height: measured.height };
			const definition = resolveDefinition(node.type, node.typeVersion, definitions);
			if (definition && isAnnotation(definition)) annotations.push(node.id);
		}

		replaceDraft(tidyDocument(draft, { sizes, annotations }));
		// Both Tidy buttons have to land the user on the result, and the toolbar
		// one used to leave the view where it was.
		await tick();
		await flow?.fitView({ padding: 0.15, duration: 300 });
	}

	/** Half a 68px tile: the offset that centres a tile on a point. */
	const TILE_CENTRE = 34;

	/** Where a node added with no source belongs: the middle of what the user can see. */
	function viewportPlacement(index: number): { x: number; y: number } {
		const rect = canvasRegion?.getBoundingClientRect();
		if (!flow || !rect) return nextNodePosition(index);
		const centre = flow.screenToFlowPosition({ x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 });
		return { x: Math.round(centre.x - TILE_CENTRE), y: Math.round(centre.y - TILE_CENTRE) };
	}

	function undo() {
		if (locked) return;
		const step = undoHistory(undoStack, draft);
		if (!step) return;
		undoStack = step.history;
		draft = step.state;
		forgetMissingSelection();
	}

	function redo() {
		if (locked) return;
		const step = redoHistory(undoStack, draft);
		if (!step) return;
		undoStack = step.history;
		draft = step.state;
		forgetMissingSelection();
	}

	async function copySelectionToClipboard() {
		const json = copySelection(draft, selectedNodeIDs);
		if (!json) {
			canvasMessage = m.editor_select_node_first();
			return;
		}
		try {
			await navigator.clipboard.writeText(json);
			canvasMessage = m.editor_nodes_copied({ count: selectedNodeIDs.length });
		} catch {
			canvasMessage = m.editor_clipboard_denied_write();
		}
	}

	/**
	 * Pastes workflow JSON, from this editor or from n8n.
	 *
	 * The two shapes are the same document with different connection tables, so
	 * one reader handles both and the report says what could not be carried
	 * across — a placeholder not replacing anything, or a wire no port here
	 * accepts — rather than dropping it silently.
	 */
	function pasteText(text: string): boolean {
		const payload = readClipboard(text, definitions);
		if (!payload) return false;
		const { document: next, nodeIDs } = pasteInto(draft, payload, pasteOffset(payload.nodes));
		replaceDraft(next);
		selectedNodeIDs = nodeIDs;
		selectedNodeID = nodeIDs[0] ?? null;
		selectedEdgeID = null;
		const notes: string[] = [m.editor_nodes_pasted({ count: nodeIDs.length })];
		if (payload.unsupported.length > 0) notes.push(m.editor_placeholders_to_replace({ count: payload.unsupported.length }));
		if (payload.dropped > 0) notes.push(m.editor_connections_not_placed({ count: payload.dropped }));
		canvasMessage = `${notes.join(' · ')}.`;
		return true;
	}

	/** Centres a pasted fragment on the visible canvas, as a paste of a snippet should land. */
	function pasteOffset(nodes: WorkflowNode[]): { x: number; y: number } {
		const rect = canvasRegion?.getBoundingClientRect();
		if (!flow || !rect || nodes.length === 0) return { x: 40, y: 40 };
		const centre = flow.screenToFlowPosition({ x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 });
		return {
			x: Math.round(centre.x - Math.min(...nodes.map((node) => node.position.x))),
			y: Math.round(centre.y - Math.min(...nodes.map((node) => node.position.y)))
		};
	}

	function onPaste(event: ClipboardEvent) {
		if (locked) return;
		// A paste into a field is the field's: JSON pasted into a code editor
		// or a JSON parameter is text for that field, not nodes for the canvas.
		if (event.defaultPrevented || isTypingTarget(event.target)) return;
		const text = event.clipboardData?.getData('text/plain') ?? '';
		if (text !== '' && pasteText(text)) event.preventDefault();
	}

	async function pasteFromClipboard() {
		if (locked) return;
		try {
			const text = await navigator.clipboard.readText();
			if (!pasteText(text)) canvasMessage = m.editor_clipboard_empty();
		} catch {
			canvasMessage = m.editor_clipboard_denied_read();
		}
	}

	function duplicateSelection() {
		if (locked) return;
		const { document: next, nodeIDs } = duplicateNodes(draft, selectedNodeIDs);
		if (nodeIDs.length === 0) {
			canvasMessage = m.editor_select_node_first();
			return;
		}
		replaceDraft(next);
		selectedNodeIDs = nodeIDs;
		selectedNodeID = nodeIDs[0] ?? null;
		selectedEdgeID = null;
		canvasMessage = m.editor_nodes_duplicated({ count: nodeIDs.length });
	}

	function startRename(nodeID: string) {
		if (locked) return;
		const node = (displayed.nodes ?? []).find((candidate) => candidate.id === nodeID);
		if (!node) return;
		renameTarget = nodeID;
		renameValue = node.name;
		void tick().then(() => {
			renameInput?.focus();
			renameInput?.select();
		});
	}

	/** Rename from the inspector's title, which is where n8n renames from. */
	function renameSelected(rawName: string) {
		if (locked || !selectedNode) return;
		const taken = (draft.nodes ?? []).filter((node) => node.id !== selectedNode.id).map((node) => node.name);
		const name = uniqueNodeName(rawName, taken);
		if (name !== rawName.trim()) canvasMessage = m.editor_renamed_taken({ name });
		replaceDraft(renameNode(draft, selectedNode.id, name));
	}

	function commitRename() {
		const nodeID = renameTarget;
		renameTarget = null;
		if (!nodeID) return;
		const taken = (draft.nodes ?? []).filter((node) => node.id !== nodeID).map((node) => node.name);
		const name = uniqueNodeName(renameValue, taken);
		if (name !== renameValue.trim()) canvasMessage = m.editor_renamed_taken({ name });
		replaceDraft(renameNode(draft, nodeID, name));
		restoreFocus();
	}

	/**
	 * The canvas keymap.
	 *
	 * Bound on the window rather than on the canvas element, because focus
	 * follows the node the user selected out of the pane. Four guards: the
	 * keystroke must not belong to a field or to a button or link that
	 * activates on it, no overlay may be open, and focus has to be inside this
	 * editor — a second editor on the page must not act on the first one's
	 * keys.
	 */
	function handleShortcut(event: KeyboardEvent) {
		if (event.defaultPrevented || controlOwnsKey(event.target, event.key)) return;
		if (renameTarget || overlayOpen) return;
		const focused = globalThis.document.activeElement;
		if (focused && focused !== globalThis.document.body && editorSection && !editorSection.contains(focused)) return;
		const action = canvasShortcut(event);
		if (!action || !performShortcut(action)) return;
		event.preventDefault();
		// The status line reports the last clipboard action; any other key makes
		// it stale.
		if (action !== 'copy' && action !== 'paste' && action !== 'duplicate') canvasMessage = null;
	}

	function performShortcut(action: CanvasShortcut): boolean {
		switch (action) {
			case 'undo':
				if (!canUndo) return false;
				undo();
				return true;
			case 'redo':
				if (!canRedo) return false;
				redo();
				return true;
			case 'save':
				if (locked || !dirty || saving) return false;
				void save();
				return true;
			case 'select-all':
				if ((displayed.nodes?.length ?? 0) === 0) return false;
				selectAll();
				return true;
			case 'copy':
				if (selectedNodeIDs.length === 0) return false;
				void copySelectionToClipboard();
				return true;
			case 'paste':
				if (locked) return false;
				void pasteFromClipboard();
				return true;
			case 'duplicate':
				if (locked || selectedNodeIDs.length === 0) return false;
				duplicateSelection();
				return true;
			case 'rename':
				if (!selectedNodeID) return false;
				startRename(selectedNodeID);
				return true;
			case 'open-selection':
				if (!selectedNodeID) return false;
				propertyPanelOpen = true;
				return true;
			case 'add-step':
				if (locked) return false;
				openPicker(false);
				return true;
			case 'tidy':
				if (locked) return false;
				void tidyUp();
				return true;
			case 'fit-view':
				if (!flow) return false;
				void flow.fitView({ padding: 0.15, duration: 200 });
				return true;
			case 'reset-zoom':
				if (!flow) return false;
				void flow.setZoom(1, { duration: 200 });
				return true;
			case 'zoom-in':
				if (!flow) return false;
				void flow.zoomIn({ duration: 150 });
				return true;
			case 'zoom-out':
				if (!flow) return false;
				void flow.zoomOut({ duration: 150 });
				return true;
			case 'help':
				shortcutsOpen = true;
				return true;
		}
	}

	async function save() {
		if (locked || !dirty || saving) return;
		await onSave(toWorkflowInput(draft));
	}

	async function run() {
		if (hideRun || dirty || running || previewing) return;
		const intent = executeIntent(displayed.nodes ?? [], definitions, selectedNodeIDs);
		if (intent.action === 'open-chat') {
			chatOpen = true;
			return;
		}
		try {
			await onRun(intent.triggerNodeId ? { triggerNodeId: intent.triggerNodeId } : undefined);
		} catch {
			// The host records runError; Execute must not become an unhandled rejection.
		}
	}

	const chatNodes = $derived((displayed.nodes ?? []).map((node) => ({ id: node.id, name: node.name })));
	const chatMemory = $derived(chatHasMemory(displayed.nodes, displayed.connections));

	async function sendChat(payload: ChatSendPayload, onEvent: (event: ExecutionEvent) => void): Promise<ExecutionResource> {
		const trigger = chatTrigger;
		if (!trigger) {
			throw new Error(m.editor_chat_send_failed({ error: m.editor_chat_last_node() }));
		}
		// The chat tests what is on the canvas, as n8n's does: unsaved edits are
		// saved first rather than silently chatting with the previous revision.
		if (dirty) {
			await save();
			await tick();
			if (dirty) throw new Error(m.editor_chat_save_failed());
		}
		const execution = await onRun({ triggerNodeId: trigger.id, input: payload, onEvent });
		if (!execution) {
			throw new Error(m.editor_chat_send_failed({ error: m.workflows_run_watch_stopped() }));
		}
		return execution;
	}

	async function toggleActivation() {
		if (!canActivate || activating) return;
		if (active) {
			await onDeactivate?.();
			return;
		}
		if (activationBlockedByDirty) return;
		await onActivate?.();
	}
</script>

<svelte:window onkeydown={handleShortcut} />

<section bind:this={editorSection} onpaste={onPaste} class="relative flex h-full min-h-0 flex-col bg-background" aria-label={m.editor_aria_label()}>
	<header class="flex h-9 shrink-0 items-center gap-1 overflow-x-auto border-b border-border bg-card px-1.5">
		{#if header}
			{@render header()}
			<span aria-hidden="true" class="mx-0.5 h-4 w-px shrink-0 bg-border"></span>
		{/if}
		{#if !locked}
			<button type="button" class="inline-flex h-7 shrink-0 items-center gap-1 whitespace-nowrap rounded-md bg-primary px-2 text-xs font-medium text-primary-foreground transition-opacity hover:opacity-90 focus-visible:outline-2 focus-visible:outline-offset-2" onclick={() => openPicker(false)}>
				<Plus aria-hidden="true" class="size-3.5" />{m.editor_add_step()}
			</button>
		{/if}
		{#if !locked}
			<!-- Undo/redo sit beside Tidy because they are what makes Tidy safe to
			     press on an imported template. -->
			<button type="button" class="grid size-7 shrink-0 place-items-center rounded-md border border-border transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-2 disabled:opacity-40" disabled={!canUndo} title={m.editor_undo_title()} aria-label={m.editor_undo()} data-testid="undo-toolbar" onclick={undo}>
				<Undo2 aria-hidden="true" class="size-3.5" />
			</button>
			<button type="button" class="grid size-7 shrink-0 place-items-center rounded-md border border-border transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-2 disabled:opacity-40" disabled={!canRedo} title={m.editor_redo_title()} aria-label={m.editor_redo()} data-testid="redo-toolbar" onclick={redo}>
				<Redo2 aria-hidden="true" class="size-3.5" />
			</button>
		{/if}
		{#if !locked}
			<button type="button" class="grid size-7 shrink-0 place-items-center rounded-md border border-border transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-2" title={m.editor_tidy_up()} aria-label={m.editor_tidy_up()} data-testid="tidy-up-toolbar" onclick={tidyUp}>
				<WandSparkles aria-hidden="true" class="size-3.5" />
			</button>
		{/if}
		{#if !locked && !hideSave}
			<button type="button" class="grid size-7 shrink-0 place-items-center rounded-md border border-border transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-2 disabled:opacity-40" disabled={!dirty || saving} title={saving ? m.editor_saving() : m.common_save()} aria-label={saving ? m.editor_saving() : m.common_save()} onclick={() => void save()}>
				<Save aria-hidden="true" class="size-3.5" />
			</button>
		{/if}
		{#if !hideRun}
			<!-- Running is refused while a past revision is on screen: the run
			     would execute the saved draft, not the graph being looked at,
			     which is the one thing a preview must never be mistaken for.
			     Primary brand CTA — n8n-style prominence without orange. -->
			<button type="button" data-testid="toolbar-run" class="inline-flex h-7 shrink-0 items-center gap-1 whitespace-nowrap rounded-md bg-primary px-2.5 text-xs font-semibold text-primary-foreground shadow-sm transition-opacity hover:opacity-90 focus-visible:outline-2 focus-visible:outline-offset-2 disabled:cursor-not-allowed disabled:opacity-40" disabled={dirty || running || previewing} aria-describedby={dirty ? 'save-first-hint' : undefined} onclick={() => void run()}>
				<Play aria-hidden="true" class="size-3.5" />{running ? m.editor_running() : m.editor_execute()}
			</button>
		{/if}
		{#if header}
			<!-- Dashboard-only: embed hosts own execution UX via host events. -->
			<a href="/executions" data-testid="open-executions" class="grid size-7 shrink-0 place-items-center rounded-md border border-border transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-2" title={m.editor_open_executions()} aria-label={m.editor_open_executions()}>
				<Activity aria-hidden="true" class="size-3.5" />
			</a>
		{/if}
		{#if history}
			<button type="button" class="grid size-7 shrink-0 place-items-center rounded-md border border-border transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-2" title={m.editor_history()} aria-label={m.editor_history()} aria-haspopup="dialog" aria-expanded={historyOpen} onclick={() => (historyOpen = true)}>
				<History aria-hidden="true" class="size-3.5" />
			</button>
		{/if}
		<button type="button" class="grid size-7 shrink-0 place-items-center rounded-md border border-border transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-2" title={m.editor_keyboard_shortcuts()} aria-label={m.editor_keyboard_shortcuts()} aria-haspopup="dialog" aria-expanded={shortcutsOpen} onclick={() => (shortcutsOpen = true)}>
			<Keyboard aria-hidden="true" class="size-3.5" />
		</button>
		{#if canActivate}
			<button type="button" class="grid size-7 shrink-0 place-items-center rounded-md border border-border transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-2 disabled:cursor-not-allowed disabled:opacity-40" disabled={activating || activationBlockedByDirty} title={activating ? (active ? m.workflows_deactivating() : m.workflows_activating()) : active ? m.workflows_deactivate() : m.workflows_activate()} aria-label={activating ? (active ? m.workflows_deactivating() : m.workflows_activating()) : active ? m.workflows_deactivate() : m.workflows_activate()} aria-describedby={activationBlockedByDirty ? 'save-first-hint' : undefined} onclick={() => void toggleActivation()}>
				{#if active}<PowerOff aria-hidden="true" class="size-3.5" />{:else}<Power aria-hidden="true" class="size-3.5" />{/if}
			</button>
		{/if}
		{#if !locked && selectedEdgeID && !selectedNodeID}
			<button type="button" class="inline-flex h-7 shrink-0 items-center gap-1 whitespace-nowrap rounded-md px-2 text-xs font-medium text-destructive transition-colors hover:bg-destructive/10 focus-visible:outline-2 focus-visible:outline-offset-2" aria-label={m.editor_delete_connection()} onclick={removeSelectedConnection}>
				<Trash2 aria-hidden="true" class="size-3.5" />{m.workflows_delete()}
			</button>
		{/if}
		<span class="ml-auto flex shrink-0 items-center gap-1 pr-1 text-[0.6875rem] text-muted-foreground" aria-live="polite">
			<!-- Whether the workflow is live is the state an activation notice is
			     about, so it is stated here rather than left to be inferred from
			     the button's label. -->
			{#if canActivate}
				<span aria-hidden="true" class="size-1.5 rounded-full {active ? 'bg-success' : 'bg-muted-foreground/40'}"></span>
				<span class="whitespace-nowrap">{active ? m.workflows_state_active() : m.editor_state_inactive()}</span>
				<span aria-hidden="true" class="mx-0.5 h-3 w-px bg-border"></span>
			{/if}
			{#if dirty && !locked}<span aria-hidden="true" class="size-1.5 rounded-full bg-warning"></span>{/if}
			<span class="hidden whitespace-nowrap sm:inline">{canvasStatus}</span>
			<span class="sr-only sm:hidden">{canvasStatus}</span>
		</span>
		{#if dirty}<span id="save-first-hint" class="sr-only">{m.editor_save_first_hint()}</span>{/if}
	</header>

	{#if saveConflict}
		<!-- Both answers are offered because the editor cannot choose: keeping
		     this draft may overwrite a colleague's, and reloading discards the
		     user's own edits. -->
		<div role="alert" class="flex shrink-0 flex-wrap items-center gap-2 border-b border-warning/30 bg-warning/10 px-3 py-1.5 text-xs">
			<span class="min-w-0 flex-1">{m.editor_conflict_notice()}</span>
			<button type="button" class="shrink-0 rounded-md border border-border bg-background px-2 py-0.5 font-medium transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-1" onclick={() => onReloadConflict?.()}>{m.workflows_reload_theirs()}</button>
			<button type="button" class="shrink-0 rounded-md border border-border bg-background px-2 py-0.5 font-medium transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-1" onclick={() => onOverwriteConflict?.()}>{m.editor_save_mine_anyway()}</button>
		</div>
	{/if}
	{#if saveError}
		<p role="alert" class="shrink-0 border-b border-destructive/25 bg-destructive/5 px-3 py-1.5 text-xs text-destructive">{m.editor_save_failed({ error: saveError })}</p>
	{/if}
	{#if canvasMessage}
		<p role="status" class="shrink-0 border-b border-border bg-muted/40 px-3 py-1.5 text-xs text-muted-foreground">
			{canvasMessage}
			<button type="button" class="ml-2 underline underline-offset-2" onclick={() => (canvasMessage = null)}>{m.editor_dismiss()}</button>
		</p>
	{/if}
	<!-- A publish refused by the compiler produces the same structured problem a
	     refused save does, so both are rendered by this one list. A second list
	     beside it would be the same information with a different way to click
	     through to the node at fault. -->
	{#if issues.length > 0}
		<ul aria-label={m.editor_validation_issues_aria()} class="shrink-0 divide-y divide-destructive/10 border-b border-destructive/20 bg-destructive/5">
			<!-- Keyed by position as well as identity: the compiler can report two
			     issues for one node with the same code, and a duplicate key crashed
			     the render (each_key_duplicate) rather than showing both. -->
			{#each issues as issue, index (`${issue.code ?? ''}-${issue.nodeID ?? issue.connectionID ?? ''}-${index}`)}
				<li><button type="button" class="w-full px-3 py-1.5 text-left text-xs text-destructive underline decoration-destructive/30 underline-offset-2 hover:decoration-destructive" onclick={() => focusValidationIssue(issue)}>{issue.message}{#if issue.nodeID} {m.editor_issue_node()}{:else if issue.connectionID} {m.editor_issue_connection()}{/if}</button></li>
			{/each}
		</ul>
	{/if}
	{#if runError}
		<p role="alert" class="shrink-0 border-b border-destructive/25 bg-destructive/5 px-3 py-1.5 text-xs text-destructive">
			{m.editor_run_failed({ error: runError })}
			{#if lastExecutionId}
				<a class="ml-2 font-medium underline underline-offset-2" href={`/executions/${lastExecutionId}`}>{m.editor_view_execution()}</a>
			{/if}
		</p>
	{:else if runMessage}
		<p role="status" class="flex shrink-0 items-center gap-2 border-b border-success/25 bg-success/5 px-3 py-1.5 text-xs text-success">
			<span>{runMessage}</span>
			{#if lastExecutionId}
				<a class="font-medium text-primary underline underline-offset-2" href={`/executions/${lastExecutionId}`}>{m.editor_view_execution()}</a>
			{/if}
		</p>
	{/if}
	<!-- A failed activation is a failure, not a notice: the workflow is not
	     listening, and it belongs in the destructive register beside the other
	     things that did not happen. -->
	{#if activationError}
		<p role="alert" class="shrink-0 border-b border-destructive/25 bg-destructive/5 px-3 py-1.5 text-xs text-destructive">{m.workflows_activation_failed({ message: activationError })}</p>
	{/if}
	<ActivationNotices {notices} onDismiss={(key) => onDismissNotice?.(key)} />

	<!-- A preview looks exactly like the editor, so it has to say that it is one
	     and offer the way back in the same breath. Without this strip the only
	     signal is that every control quietly stopped working. -->
	{#if preview}
		<div role="status" class="flex shrink-0 items-center gap-2 border-b border-primary/25 bg-primary/5 px-3 py-1.5 text-xs">
			<History aria-hidden="true" class="size-3.5 shrink-0 text-primary" />
			<span class="min-w-0 flex-1 truncate">{m.editor_previewing_note({ revision: preview.revision })}</span>
			<button type="button" class="shrink-0 rounded-md border border-border bg-background px-2 py-0.5 text-xs font-medium transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-1" onclick={() => (preview = null)}>
				{m.versions_back_to_draft()}
			</button>
		</div>
	{/if}

	<div class="relative flex min-h-0 flex-1 flex-col lg:grid" style={showInspector || selectedUncatalogued ? 'grid-template-columns: minmax(0,1fr) 20rem' : 'grid-template-columns: minmax(0,1fr)'}>
		<!-- tabindex makes this a place focus can land after a node is deleted; -1
		     keeps it out of the tab sequence. -->
		<div bind:this={canvasRegion} tabindex="-1" class="relative min-h-0 flex-1 overflow-hidden outline-none" data-testid="workflow-canvas">
			<SvelteFlow
				bind:nodes
				bind:edges
				{nodeTypes}
				{edgeTypes}
				fitView
				fitViewOptions={{ padding: 0.15, maxZoom: 1 }}
				minZoom={0.1}
				onlyRenderVisibleElements
				nodesDraggable={!locked}
				nodesConnectable={!locked}
				deleteKey={locked ? null : CANVAS_DELETE_KEYS}
				isValidConnection={(connection) => canConnect(connection, draft.nodes ?? [], definitions, draft.connections ?? [])}
				onconnect={onConnect}
				onconnectstart={onConnectStart}
				onconnectend={onConnectEnd}
				ondelete={onDelete}
				onnodedragstop={syncCanvas}
				onselectionchange={onSelectionChange}
				ariaLabelConfig={ariaLabels}
				onpaneclick={() => onSelectionChange({ nodes: [], edges: [] })}
			>
				<Background variant={BackgroundVariant.Dots} gap={16} size={1} patternColor="var(--border)" />
				<EditorControls locked={locked} onTidy={tidyUp} />
				<!-- Svelte Flow owns the viewport and only a component inside it can
				     read that context; this hands the editor the handful of calls it
				     needs and renders nothing. -->
				<CanvasBridge onReady={(next) => (flow = next)} />
				<!-- A minimap over a few nodes is noise; on a 63-node import it is
				     the only way to know where you are. -->
				{#if (displayed.nodes?.length ?? 0) > 12 && !narrow.current}
					<MiniMap
						position="bottom-left"
						pannable
						zoomable
						ariaLabel={m.editor_minimap_aria()}
						bgColor="var(--card)"
						maskColor="color-mix(in oklch, var(--background) 70%, transparent)"
						nodeColor="var(--border)"
						nodeStrokeWidth={2}
					/>
				{/if}
				{#if renameTarget}
					{@const renaming = (displayed.nodes ?? []).find((node) => node.id === renameTarget)}
					{#if renaming}
						<!-- Rendered in the flow's own coordinate space so the field sits on
						     the tile it renames, at whatever zoom the user is at. -->
						<ViewportPortal target="front">
							<div class="nodrag nopan absolute" style={`left: ${renaming.position.x - 60}px; top: ${renaming.position.y - 8}px; width: 10rem`}>
								<label class="sr-only" for="node-rename-input">{m.editor_node_name_label()}</label>
								<input
									id="node-rename-input"
									bind:this={renameInput}
									bind:value={renameValue}
									class="h-7 w-full rounded-md border border-primary bg-background px-2 text-xs shadow-md outline-none"
									onkeydown={(event) => {
										if (event.key === 'Enter') {
											event.preventDefault();
											commitRename();
										}
										if (event.key === 'Escape') {
											event.preventDefault();
											renameTarget = null;
											restoreFocus();
										}
									}}
									onblur={commitRename}
								/>
							</div>
						</ViewportPortal>
					{/if}
				{/if}
			</SvelteFlow>

			{#if showChat && chatTrigger}
				<div class="pointer-events-none absolute bottom-4 left-4 z-20">
					<!-- Kept mounted while the trigger is on the canvas so sessionId
					     and history survive Close. New chat is what rotates the id. -->
					<div class="pointer-events-auto {chatOpen ? '' : 'hidden'}">
						<CanvasChatPanel
							triggerNodeId={chatTrigger.id}
							open={chatOpen}
							nodes={chatNodes}
							hasMemory={chatMemory}
							{dirty}
							busy={running}
							onClose={() => (chatOpen = false)}
							onSend={sendChat}
							onFocusIssue={focusValidationIssue}
						/>
					</div>
					{#if !chatOpen}
						<button
							type="button"
							data-testid="workflow-chat-button"
							class="pointer-events-auto inline-flex h-10 items-center gap-2 rounded-full border border-border bg-card px-4 text-sm font-semibold text-foreground shadow-lg shadow-black/20 transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-2"
							onclick={() => (chatOpen = true)}
							aria-label={m.editor_chat_open()}
						>
							<MessageCircle aria-hidden="true" class="size-4" />
							{m.editor_chat()}
						</button>
					{/if}
				</div>
			{/if}

			{#if !hideRun && (displayed.nodes?.length ?? 0) > 0}
				<!-- n8n-style bottom-center execute affordance; same guards as toolbar Run.
				     Mobile-only: on md+ the compact toolbar Execute covers it. -->
				<div class="pointer-events-none absolute inset-x-0 bottom-4 z-10 flex justify-center px-3 md:hidden">
					<button
						type="button"
						data-testid="canvas-execute"
						class="pointer-events-auto inline-flex h-10 items-center gap-2 rounded-full bg-primary px-5 text-sm font-semibold text-primary-foreground shadow-lg shadow-black/40 transition-opacity hover:opacity-90 focus-visible:outline-2 focus-visible:outline-offset-2 disabled:cursor-not-allowed disabled:opacity-40"
						disabled={dirty || running || previewing}
						aria-describedby={dirty ? 'save-first-hint' : undefined}
						onclick={() => void run()}
					>
						<Play aria-hidden="true" class="size-4" />
						{running ? m.editor_running() : dirty ? m.editor_save_to_execute() : m.editor_execute_workflow()}
					</button>
				</div>
			{/if}

			{#if (displayed.nodes?.length ?? 0) === 0 && !locked}
				<div class="pointer-events-none absolute inset-0 grid place-items-center p-4">
					<div class="pointer-events-auto text-center">
						<button type="button" class="mx-auto grid h-17 w-17 place-items-center rounded-l-[2.125rem] rounded-r-lg border border-dashed border-border bg-card text-muted-foreground transition-colors hover:border-primary hover:text-primary focus-visible:outline-2 focus-visible:outline-offset-4" aria-label={m.editor_add_first_step()} onclick={() => openPicker(true)}>
							<Plus aria-hidden="true" class="size-5" />
						</button>
						<h2 class="mt-3 text-sm font-semibold">{m.editor_empty_title()}</h2>
						<p class="mt-1 max-w-56 text-xs leading-5 text-muted-foreground">{m.editor_empty_body()}</p>
					</div>
				</div>
			{/if}
		</div>

		{#if showInspector && selectedNode && selectedDefinition}
			<aside bind:this={inspectorRegion} tabindex="-1" class="hidden min-h-0 border-l border-border outline-none lg:block">
				<div class="flex h-full min-h-0 flex-col">
					<div class="min-h-0 flex-1">
						<PropertiesPanel node={selectedNode} definition={selectedDefinition} {credentials} readOnly={locked} onChange={updateProperty} onRename={renameSelected} onCredentialChange={updateCredential} />
					</div>
					{#if selectedResolvedVersion !== null && selectedResolvedVersion !== selectedNode.typeVersion}
						<p class="shrink-0 border-t border-border px-3 py-1.5 text-[0.6875rem] leading-4 text-muted-foreground">{m.editor_resolved_version({ stored: selectedNode.typeVersion, resolved: selectedResolvedVersion })}</p>
					{/if}
				</div>
			</aside>
		{/if}
		{#if selectedUncatalogued && selectedNode}
			<aside tabindex="-1" class="hidden min-h-0 overflow-y-auto border-l border-border bg-card outline-none lg:block">
				<section aria-label={m.editor_node_details_aria({ name: selectedNode.name })} class="flex h-full min-h-0 flex-col p-3">
					<p class="flex items-center gap-1.5 text-xs font-semibold">
						<TriangleAlert aria-hidden="true" class="size-3.5 shrink-0 text-destructive" />
						{capsuleOrigin ? m.editor_unsupported_node() : m.editor_unknown_node_type()}
					</p>
					<p class="mt-1 truncate text-[0.8125rem] font-medium">{selectedNode.name}</p>
					{#if capsuleOrigin}
						<dl class="mt-2 grid gap-1 rounded-lg border border-border bg-muted/40 px-2.5 py-2 font-mono text-[0.6875rem] leading-5">
							<div class="flex min-w-0 gap-2"><dt class="shrink-0 text-muted-foreground">type</dt><dd class="min-w-0 flex-1 truncate">{capsuleOrigin.type}</dd></div>
							{#if capsuleOrigin.version !== undefined && capsuleOrigin.version !== null}
								<div class="flex min-w-0 gap-2"><dt class="shrink-0 text-muted-foreground">version</dt><dd class="min-w-0 flex-1 truncate">{String(capsuleOrigin.version)}</dd></div>
							{/if}
						</dl>
						<p class="mt-2 text-xs leading-5 text-muted-foreground">{m.editor_unsupported_body()}</p>
						<p class="mt-1 text-xs leading-5 text-muted-foreground">{m.editor_unsupported_preserved()}</p>
					{:else}
						<p class="mt-2 text-xs leading-5 text-muted-foreground">{m.editor_unknown_body()}</p>
					{/if}
				</section>
			</aside>
		{/if}

		{#if narrow.current && propertyPanelOpen && selectedNode && selectedDefinition}
			<div bind:this={propertyDialog} class="absolute inset-x-2 bottom-2 z-30 max-h-[min(28rem,calc(100%-1rem))] overflow-hidden rounded-xl border border-border bg-card shadow-xl" role="dialog" aria-modal="true" aria-label={m.editor_node_properties_aria({ name: selectedNode.name })} tabindex="-1" onkeydown={handlePropertyDialogKeydown}>
				<div class="flex justify-end border-b border-border px-1.5 py-1"><button bind:this={propertyCloseButton} type="button" class="grid size-6 place-items-center rounded-md text-muted-foreground hover:bg-muted" aria-label={m.editor_close_properties_aria()} onclick={closePropertyPanel}><X aria-hidden="true" class="size-3.5" /></button></div>
				<PropertiesPanel node={selectedNode} definition={selectedDefinition} {credentials} readOnly={locked} onChange={updateProperty} onRename={renameSelected} onCredentialChange={updateCredential} />
				{#if selectedResolvedVersion !== null && selectedResolvedVersion !== selectedNode.typeVersion}
					<p class="border-t border-border px-3 py-1.5 text-[0.6875rem] leading-4 text-muted-foreground">{m.editor_resolved_version({ stored: selectedNode.typeVersion, resolved: selectedResolvedVersion })}</p>
				{/if}
			</div>
		{/if}
		{#if narrow.current && propertyPanelOpen && selectedUncatalogued && selectedNode}
			<div class="absolute inset-x-2 bottom-2 z-30 max-h-[min(28rem,calc(100%-1rem))] overflow-hidden rounded-xl border border-border bg-card shadow-xl" role="dialog" aria-modal="true" aria-label={m.editor_node_details_aria({ name: selectedNode.name })} tabindex="-1" onkeydown={handlePropertyDialogKeydown}>
				<div class="flex justify-end border-b border-border px-1.5 py-1"><button type="button" class="grid size-6 place-items-center rounded-md text-muted-foreground hover:bg-muted" aria-label={m.editor_close_details_aria()} onclick={closePropertyPanel}><X aria-hidden="true" class="size-3.5" /></button></div>
				<section aria-label={m.editor_node_details_aria({ name: selectedNode.name })} class="max-h-[min(24rem,calc(100%-3rem))] overflow-y-auto p-3">
					<p class="flex items-center gap-1.5 text-xs font-semibold">
						<TriangleAlert aria-hidden="true" class="size-3.5 shrink-0 text-destructive" />
						{capsuleOrigin ? m.editor_unsupported_node() : m.editor_unknown_node_type()}
					</p>
					<p class="mt-1 truncate text-[0.8125rem] font-medium">{selectedNode.name}</p>
					{#if capsuleOrigin}
						<dl class="mt-2 grid gap-1 rounded-lg border border-border bg-muted/40 px-2.5 py-2 font-mono text-[0.6875rem] leading-5">
							<div class="flex min-w-0 gap-2"><dt class="shrink-0 text-muted-foreground">type</dt><dd class="min-w-0 flex-1 truncate">{capsuleOrigin.type}</dd></div>
							{#if capsuleOrigin.version !== undefined && capsuleOrigin.version !== null}
								<div class="flex min-w-0 gap-2"><dt class="shrink-0 text-muted-foreground">version</dt><dd class="min-w-0 flex-1 truncate">{String(capsuleOrigin.version)}</dd></div>
							{/if}
						</dl>
						<p class="mt-2 text-xs leading-5 text-muted-foreground">{m.editor_unsupported_body()}</p>
					{:else}
						<p class="mt-2 text-xs leading-5 text-muted-foreground">{m.editor_unknown_body()}</p>
					{/if}
				</section>
			</div>
		{/if}
	</div>

	<NodePicker
		bind:open={pickerOpen}
		{definitions}
		{triggersOnly}
		connecting={Boolean(pendingSource)}
		providesKind={pendingKind}
		onSelect={addNode}
		onDismiss={() => {
			pendingSource = null;
			pendingKind = null;
			pendingSplice = null;
			pendingPosition = null;
		}}
	/>

	{#if shortcutsOpen}
		<div class="absolute inset-0 z-50 grid place-items-center bg-background/60 p-4 backdrop-blur-sm" role="presentation">
			<button type="button" tabindex="-1" aria-hidden="true" class="absolute inset-0 cursor-default" onclick={() => (shortcutsOpen = false)}></button>
			<div class="relative max-h-[min(32rem,calc(100dvh-2rem))] w-full max-w-sm overflow-y-auto rounded-xl border border-border bg-popover p-4 shadow-2xl" role="dialog" aria-modal="true" aria-labelledby="shortcut-title">
				<h2 id="shortcut-title" class="text-sm font-semibold">{m.editor_keyboard_shortcuts()}</h2>
				<!-- Rendered from the keymap itself, so a shortcut cannot exist in the
				     handler and be missing from the list a user reads. -->
				<dl class="mt-3 grid grid-cols-[auto_1fr] items-baseline gap-x-3 gap-y-1.5 text-xs">
					{#each SHORTCUT_REFERENCE as shortcut (shortcut.action)}
						<dt class="font-mono text-muted-foreground">{shortcut.keys}</dt>
						<dd>{shortcut.label()}</dd>
					{/each}
				</dl>
				<button type="button" class="mt-4 w-full rounded-md border border-border px-2 py-1 text-xs font-medium transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-1" onclick={() => (shortcutsOpen = false)}>{m.common_close()}</button>
			</div>
		</div>
	{/if}

	{#if history}
		<!-- Mounted here rather than by each host so both surfaces get the same
		     panel wired the same way, and so `dirty` — which only this component
		     knows — reaches the restore guard without a round trip. -->
		<VersionPanel
			bind:open={historyOpen}
			bind:preview
			workflowID={history.workflowID}
			{draft}
			{dirty}
			latestVersionID={history.latestVersionID}
			{active}
			canRestore={history.canRestore && !readOnly}
			canPublish={history.canPublish}
			onRestored={history.onRestored}
			onPublished={history.onPublished}
			onUnpublished={history.onUnpublished}
			onIssues={(next) => (historyIssues = next)}
		/>
	{/if}
</section>
