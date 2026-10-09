import type { Component } from 'svelte';

import Archive from '@lucide/svelte/icons/archive';
import ArrowDownUp from '@lucide/svelte/icons/arrow-down-up';
import ArrowRight from '@lucide/svelte/icons/arrow-right';
import Axis3d from '@lucide/svelte/icons/axis-3d';
import Bot from '@lucide/svelte/icons/bot';
import Box from '@lucide/svelte/icons/box';
import Boxes from '@lucide/svelte/icons/boxes';
import Braces from '@lucide/svelte/icons/braces';
import Brain from '@lucide/svelte/icons/brain';
import Calculator from '@lucide/svelte/icons/calculator';
import CalendarClock from '@lucide/svelte/icons/calendar-clock';
import CircleHelp from '@lucide/svelte/icons/circle-help';
import ClipboardList from '@lucide/svelte/icons/clipboard-list';
import Clock from '@lucide/svelte/icons/clock';
import Code from '@lucide/svelte/icons/code';
import CodeXml from '@lucide/svelte/icons/code-xml';
import CopyMinus from '@lucide/svelte/icons/copy-minus';
import CornerDownLeft from '@lucide/svelte/icons/corner-down-left';
import Database from '@lucide/svelte/icons/database';
import DatabaseSearch from '@lucide/svelte/icons/database-search';
import FileOutput from '@lucide/svelte/icons/file-output';
import Filter from '@lucide/svelte/icons/filter';
import Folder from '@lucide/svelte/icons/folder';
import Globe from '@lucide/svelte/icons/globe';
import Group from '@lucide/svelte/icons/group';
import HardDrive from '@lucide/svelte/icons/hard-drive';
import Layers from '@lucide/svelte/icons/layers';
import Link from '@lucide/svelte/icons/link';
import ListEnd from '@lucide/svelte/icons/list-end';
import Mail from '@lucide/svelte/icons/mail';
import MemoryStick from '@lucide/svelte/icons/memory-stick';
import Merge from '@lucide/svelte/icons/merge';
import MessageCircle from '@lucide/svelte/icons/message-circle';
import MessagesSquare from '@lucide/svelte/icons/messages-square';
import MousePointerClick from '@lucide/svelte/icons/mouse-pointer-click';
import OctagonAlert from '@lucide/svelte/icons/octagon-alert';
import Pause from '@lucide/svelte/icons/pause';
import PencilLine from '@lucide/svelte/icons/pencil-line';
import Repeat from '@lucide/svelte/icons/repeat';
import Route from '@lucide/svelte/icons/route';
import Scissors from '@lucide/svelte/icons/scissors';
import Send from '@lucide/svelte/icons/send';
import Server from '@lucide/svelte/icons/server';
import Sigma from '@lucide/svelte/icons/sigma';
import Signpost from '@lucide/svelte/icons/signpost';
import Sparkles from '@lucide/svelte/icons/sparkles';
import Split from '@lucide/svelte/icons/split';
import SquareTerminal from '@lucide/svelte/icons/square-terminal';
import StickyNote from '@lucide/svelte/icons/sticky-note';
import Table from '@lucide/svelte/icons/table';
import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
import Ungroup from '@lucide/svelte/icons/ungroup';
import Webhook from '@lucide/svelte/icons/webhook';
import Workflow from '@lucide/svelte/icons/workflow';
import Wrench from '@lucide/svelte/icons/wrench';

import type { Definition, Node as WorkflowNode, Port } from '$lib/api/generated/models';
import { isExpression } from './parameter';

/**
 * A node's silhouette on the canvas, and the one thing a reader learns before
 * reading any text.
 *
 * - `trigger` — a rounded left edge. The flow enters here.
 * - `step` — a square. It runs in sequence.
 * - `hub` — a wide pill. It runs in sequence and takes attachments underneath.
 * - `attachment` — a circle. It configures another node rather than running in
 *   line with the items.
 *
 * Every shape is derived from the ports the registry declares, never from a
 * list of known type names, so a node type added on the server arrives with the
 * right silhouette and no frontend change.
 */
export type NodeShape = 'trigger' | 'step' | 'hub' | 'attachment';

export type NodeVisual = {
	icon: Component;
	/** Set when the node ships its own artwork, which is rendered instead. */
	iconURL: string | null;
	/** A colour or CSS custom property reference, applied as `--node-accent`. */
	accent: string;
	shape: NodeShape;
};

/**
 * Glyphs this editor ships, keyed by the name a definition asks for.
 *
 * This is not the old type-to-icon map wearing a new hat. That map keyed on the
 * *node type*, so a node the editor had never heard of got a grey box; this
 * keys on a name the **server** chose, so a node added on the server picks a
 * glyph without a frontend change. A node whose artwork is not a builtin glyph
 * ships its own bytes and is served by the icon route instead.
 */
const GLYPHS: Record<string, Component> = {
	archive: Archive,
	'arrow-down-up': ArrowDownUp,
	'arrow-right': ArrowRight,
	'axis-3d': Axis3d,
	bot: Bot,
	box: Box,
	boxes: Boxes,
	braces: Braces,
	brain: Brain,
	// An older name the server may still send for the alert glyph; both map to
	// the same lucide icon so a cached catalogue and a fresh one agree.
	'alert-triangle': TriangleAlert,
	'triangle-alert': TriangleAlert,
	'octagon-alert': OctagonAlert,
	'calendar-clock': CalendarClock,
	calculator: Calculator,
	clock: Clock,
	code: Code,
	'code-xml': CodeXml,
	'copy-minus': CopyMinus,
	'circle-help': CircleHelp,
	'clipboard-list': ClipboardList,
	database: Database,
	'database-search': DatabaseSearch,
	'file-output': FileOutput,
	filter: Filter,
	folder: Folder,
	form: ClipboardList,
	'git-branch': Split,
	'git-merge': Merge,
	globe: Globe,
	group: Group,
	'hard-drive': HardDrive,
	layers: Layers,
	link: Link,
	'list-end': ListEnd,
	mail: Mail,
	'message-circle': MessageCircle,
	'messages-square': MessagesSquare,
	'memory-stick': MemoryStick,
	'mouse-pointer-click': MousePointerClick,
	pause: Pause,
	pencil: PencilLine,
	reply: CornerDownLeft,
	repeat: Repeat,
	route: Route,
	scissors: Scissors,
	send: Send,
	server: Server,
	sigma: Sigma,
	signpost: Signpost,
	sparkles: Sparkles,
	'square-terminal': SquareTerminal,
	'sticky-note': StickyNote,
	table: Table,
	ungroup: Ungroup,
	webhook: Webhook,
	workflow: Workflow,
	wrench: Wrench
};

/**
 * The names the editor can draw, and the JSON list that keeps the two sides
 * honest: the server test (nodes/glyphs_test.go) refuses a definition naming a
 * builtin glyph absent from glyphs.json, and the frontend test refuses a
 * glyphs.json entry absent from this map. Adding an icon means touching the
 * JSON and both tests, never the canvas.
 */
export const BUILTIN_GLYPHS = GLYPHS;

/** The prefix that marks a glyph the editor already imports. */
const BUILTIN_PREFIX = 'builtin:';

/**
 * The fallback glyph, used when a node names one this build does not ship.
 *
 * Deliberately distinguishable from a real icon: it means "this editor is older
 * than this node", which is a different thing from "this node looks like a
 * box", and a user seeing it should be able to tell.
 */
export const FALLBACK_GLYPH = Box;

/** Tile geometry per silhouette, shared by the editor and the replay canvas.
 *
 * Compact default (n8n-like density): a step/trigger tile is 4.25rem (68px)
 * square so long chains fit, with hub/attachment scaled proportionally. The
 * name band below stays wider than the tile; layout.ts accounts for it. */
export const TILE: Record<NodeShape, string> = {
	trigger: 'h-17 w-17 rounded-l-[2.125rem] rounded-r-lg',
	step: 'h-17 w-17 rounded-lg',
	hub: 'h-14 min-w-36 max-w-60 gap-2 rounded-xl px-3',
	attachment: 'size-12 rounded-full'
};

/** Glyph size per silhouette. A hub and an attachment carry a smaller icon. */

/**
 * Selection / validation / run cues for a canvas tile. Shared by the editor and
 * the replay node so both surfaces paint the same chrome from the same rules.
 */
export type NodeChromeState = {
	selected?: boolean;
	invalid?: boolean;
	/** Live or replayed run status when the surface knows one. */
	runStatus?: string | null;
};

/** Border colour for the tile given selection, validation, and run state. */
export function nodeChromeBorder(state: NodeChromeState): string {
	if (state.invalid || state.runStatus === 'failed') return 'var(--destructive)';
	if (state.runStatus === 'running' || state.runStatus === 'cancelling') return 'var(--primary)';
	if (state.runStatus === 'succeeded') return 'var(--success)';
	// One selection colour on every tile, whatever its category or brand colour
	// (BUG-ywbvfa); it follows an embed host's accent through --kf-accent-src.
	if (state.selected) return 'var(--kf-selection)';
	// Muted accent hue — readable at low zoom without competing with selection.
	return 'oklch(from var(--node-accent) 0.42 0.045 h)';
}

/** Elevation and focus ring for the tile. */
export function nodeChromeShadow(state: NodeChromeState): string {
	if (state.invalid || state.runStatus === 'failed') {
		return '0 0 0 2px color-mix(in oklch, var(--destructive) 42%, transparent), 0 2px 8px oklch(0 0 0 / 40%)';
	}
	if (state.runStatus === 'running' || state.runStatus === 'cancelling') {
		return '0 0 0 2px color-mix(in oklch, var(--primary) 45%, transparent), 0 2px 10px oklch(0 0 0 / 42%)';
	}
	if (state.selected) return '0 0 0 2px var(--kf-selection-halo), var(--kf-shadow-1)';
	// Soft card lift so nodes read as tiles rather than flat glyphs when zoomed out.
	return '0 1px 3px oklch(0 0 0 / 38%), 0 0 0 1px oklch(0 0 0 / 14%)';
}

/** Icon badge geometry inside a card-like tile. Compact with the tile. */
export function nodeIconBadgeClass(shape: NodeShape): string {
	if (shape === 'attachment') return 'grid size-7 place-items-center rounded-full border';
	if (shape === 'hub') return 'grid size-8 shrink-0 place-items-center rounded-lg border';
	return 'grid size-9 place-items-center rounded-lg border';
}

export function glyphClass(shape: NodeShape): string {
	return shape === 'trigger' || shape === 'step' ? 'size-6' : 'size-4.5';
}

/** Even spacing for `count` ports along one edge of a tile, as a percentage. */
export function portOffset(index: number, count: number): string {
	return `${((index + 1) / (count + 1)) * 100}%`;
}

/**
 * How many label rows the attachment ports under a hub need, or null when
 * there are too many to label at all.
 *
 * The labels sit in rows under the tile, one row per `index % rows`; two labels
 * in the same row are `rows` slots apart, so a row fits a label when
 * `rows * slot >= labelWidth`. Sized against the narrowest hub (144px) because a
 * wider hub only adds room. The common cases — a model, a model and a memory —
 * stay on one row; four attachments would otherwise print "Chat Mode|Tools"
 * with the two labels colliding. Past six rows the stack is taller than the
 * tile it describes, so the labels drop (the handles keep their aria labels
 * and tooltips) rather than turn the hub into a wall of text.
 */
export function attachmentLabelRows(count: number, tileWidth = 144, labelWidth = 48): number | null {
	if (count <= 1) return 1;
	const slot = tileWidth / (count + 1);
	const rows = Math.ceil(labelWidth / slot);
	return rows > 6 ? null : rows;
}

/**
 * A readable name for the node type an import placeholder stands in for.
 *
 * `@n8n/n8n-nodes-langchain.toolSerpApi` becomes "Tool Serp Api": the package
 * path is dropped and the remainder is split on camel-case. The words keep
 * their original order because the leading word is often the meaningful kind
 * (tool, model, chain) and reordering would invent a taxonomy the type string
 * does not actually carry.
 */
export function friendlyOriginalType(originalType: string | null | undefined): string | null {
	if (!originalType) return null;
	const leaf = originalType.includes('.') ? (originalType.split('.').pop() ?? originalType) : originalType;
	const words = leaf
		.replace(/([a-z0-9])([A-Z])/g, '$1 $2')
		.replace(/([A-Z]+)([A-Z][a-z])/g, '$1 $2')
		.trim()
		.split(/\s+/)
		.filter(Boolean);
	if (words.length === 0) return null;
	const [first, ...rest] = words;
	return [first.charAt(0).toUpperCase() + first.slice(1), ...rest].join(' ');
}

/**
 * How one node is drawn, entirely from what the server declared.
 *
 * Nothing here keys on a node type. A type the editor has never seen arrives
 * with its own glyph or artwork, its own accent and its own subtitle, and needs
 * no frontend change — which is the whole point, because a generated pack of a
 * hundred operations is not going to get hand-written entries.
 *
 * `ports` are the node's ports as its configuration resolves them
 * (resolvedPorts). The silhouette follows those, not the type's static list: a
 * vector store is a step as the picker shows it and a hub once its mode gives it
 * embedding, document or vectorStore attachments.
 */
export function nodeVisual(definition: Definition, ports?: ResolvedPorts): NodeVisual {
	return {
		icon: builtinGlyph(definition),
		iconURL: servedIconURL(definition),
		accent: definition.iconColor || 'var(--muted-foreground)',
		shape: nodeShape(definition, ports)
	};
}

/** The lucide component a definition names, or the fallback. */
function builtinGlyph(definition: Definition): Component {
	const name = definition.icon?.light ?? '';
	if (!name.startsWith(BUILTIN_PREFIX)) return FALLBACK_GLYPH;
	return GLYPHS[name.slice(BUILTIN_PREFIX.length)] ?? FALLBACK_GLYPH;
}

/**
 * The icon route for a node that ships its own artwork, or null.
 *
 * Rendered through `<img src>` rather than inlined: SVG is an active document
 * format, the editor is embedded in customer pages, and an `<img>` gives the
 * browser's own image sandbox for free.
 */
export function servedIconURL(definition: Definition, theme = 'light'): string | null {
	const name = definition.icon?.light ?? '';
	if (!name || name.startsWith(BUILTIN_PREFIX)) return null;
	const version = encodeURIComponent(String(definition.version));
	return `/api/v1/node-types/${encodeURIComponent(definition.type)}/icon?version=${version}&theme=${encodeURIComponent(theme)}`;
}

/** A node's ports once its own configuration has been applied. */
export type ResolvedPorts = { inputs: Port[]; outputs: Port[] };

export function nodeShape(definition: Definition, ports?: ResolvedPorts): NodeShape {
	const inputs = ports?.inputs ?? definition.inputs ?? [];
	const outputs = ports?.outputs ?? definition.outputs ?? [];

	// Order matters. A tool has no inputs at all and would otherwise read as a
	// trigger, so what a node *provides* is settled before what it consumes —
	// except when it does both, which needs the width a hub has and an
	// attachment does not.
	if (inputs.some(isAttachment)) return 'hub';
	if (outputs.some(isAttachment)) return 'attachment';
	if (inputs.length === 0) return 'trigger';
	return 'step';
}

export function isAttachment(port: Port): boolean {
	return port.kind !== 'main';
}

/**
 * Whether a node annotates the canvas instead of executing.
 *
 * Decided by the behavioural group, not by the type name: the registry marks
 * the organization group "annotates the canvas and never executes", and a
 * sticky note is the member of it. A second annotation node added server-side
 * is drawn as an annotation here without a frontend change.
 */
export function isAnnotation(definition: Definition): boolean {
	return (definition.group ?? []).includes('organization');
}

export function mainPorts(ports: Port[] | null | undefined): Port[] {
	return (ports ?? []).filter((port) => !isAttachment(port));
}

export function attachmentPorts(ports: Port[] | null | undefined): Port[] {
	return (ports ?? []).filter(isAttachment);
}

/**
 * The definition an import placeholder is drawn with: only the ports its edges
 * use.
 *
 * A placeholder's definition is the widest member of the arity family the
 * import registered — main in and out plus twelve AI ports each way — three
 * times wider than the node it stands in for, with every label printed over
 * its neighbours. A placeholder never runs, so an unwired port has nothing to
 * offer. Inputs and outputs are matched separately because both sides have a
 * port called `main`: a tool wired only by its AI output keeps no main input,
 * and so draws as the sub-node circle it stands in for. Only when nothing is
 * wired at all does the tile keep a plain main in and out, so it can still be
 * wired by hand.
 */
export function placeholderDefinition(
	definition: Definition,
	wiredInputs: ReadonlySet<string | null | undefined>,
	wiredOutputs: ReadonlySet<string | null | undefined>
): Definition {
	const inputs = (definition.inputs ?? []).filter((port) => wiredInputs.has(port.name));
	const outputs = (definition.outputs ?? []).filter((port) => wiredOutputs.has(port.name));
	if (inputs.length === 0 && outputs.length === 0) {
		return {
			...definition,
			inputs: mainPorts(definition.inputs).slice(0, 1),
			outputs: mainPorts(definition.outputs).slice(0, 1)
		};
	}
	return { ...definition, inputs, outputs };
}

/**
 * The one line under a node's name.
 *
 * Rendered from the template the definition declares, over the node's own
 * parameters. It used to be a switch over eleven known node types with a null
 * default, so a node the editor had never seen simply had no subtitle.
 *
 * A missing parameter renders as nothing rather than as the word "undefined",
 * and a template that resolves to nothing at all yields null — an empty line is
 * honest about a node that is not configured yet, and is what the previous
 * default did deliberately.
 */
export function nodeSubtitle(node: WorkflowNode, definition: Definition): string | null {
	const template = definition.subtitle;
	if (!template) return null;

	const parameters = node.parameters ?? {};
	const rendered = template.replace(/\{\{\s*\$parameter\.([A-Za-z0-9_]+)\s*\}\}/g, (_, key: string) => {
		const value = parameters[key];
		// An expression is shown as the marker rather than its template: the
		// canvas cannot resolve it, and printing the raw {{ … }} twice over
		// reads as a rendering bug.
		if (isExpression(value)) return 'ƒx';
		if (value === null || value === undefined) return '';
		return typeof value === 'object' ? '' : String(value);
	});

	const trimmed = rendered
		// A template like "{{ $parameter.resource }}: {{ $parameter.operation }}"
		// with both parameters unset used to render the bare separators — a
		// Telegram tile showing a stray ":" — so leading and trailing separator
		// runs are stripped before deciding the line is empty.
		.replace(/^[\s:/|,;\-–—]+/, '')
		.replace(/[\s:/|,;\-–—]+$/, '')
		.replace(/\s+/g, ' ')
		.trim();
	return trimmed === '' || trimmed === '/' ? null : trimmed;
}
