import type { Component } from 'svelte';

import ArrowDownUp from '@lucide/svelte/icons/arrow-down-up';
import Archive from '@lucide/svelte/icons/archive';
import Bot from '@lucide/svelte/icons/bot';
import Box from '@lucide/svelte/icons/box';
import CalendarClock from '@lucide/svelte/icons/calendar-clock';
import Calculator from '@lucide/svelte/icons/calculator';
import CircleHelp from '@lucide/svelte/icons/circle-help';
import Repeat from '@lucide/svelte/icons/repeat';
import Scissors from '@lucide/svelte/icons/scissors';
import Send from '@lucide/svelte/icons/send';
import StickyNote from '@lucide/svelte/icons/sticky-note';
import Clock from '@lucide/svelte/icons/clock';
import Code from '@lucide/svelte/icons/code';
import CornerDownLeft from '@lucide/svelte/icons/corner-down-left';
import Database from '@lucide/svelte/icons/database';
import Filter from '@lucide/svelte/icons/filter';
import Globe from '@lucide/svelte/icons/globe';
import Link from '@lucide/svelte/icons/link';
import Merge from '@lucide/svelte/icons/merge';
import MessageCircle from '@lucide/svelte/icons/message-circle';
import MousePointerClick from '@lucide/svelte/icons/mouse-pointer-click';
import Pause from '@lucide/svelte/icons/pause';
import PencilLine from '@lucide/svelte/icons/pencil-line';
import Sparkles from '@lucide/svelte/icons/sparkles';
import Split from '@lucide/svelte/icons/split';
import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
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
	bot: Bot,
	box: Box,
	'calendar-clock': CalendarClock,
	calculator: Calculator,
	brain: Sparkles,
	clock: Clock,
	code: Code,
	'circle-help': CircleHelp,
	database: Database,
	'git-branch': Split,
	filter: Filter,
	'git-merge': Merge,
	globe: Globe,
	link: Link,
	'message-circle': MessageCircle,
	'memory-stick': Archive,
	'mouse-pointer-click': MousePointerClick,
	pause: Pause,
	pencil: PencilLine,
	reply: CornerDownLeft,
	repeat: Repeat,
	scissors: Scissors,
	send: Send,
	'sticky-note': StickyNote,
	webhook: Webhook,
	workflow: Workflow,
	wrench: Wrench
};

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

/** Tile geometry per silhouette, shared by the editor and the replay canvas. */
export const TILE: Record<NodeShape, string> = {
	trigger: 'h-22 w-22 rounded-l-[2.75rem] rounded-r-xl',
	step: 'h-22 w-22 rounded-xl',
	hub: 'h-18 min-w-44 max-w-72 gap-2.5 rounded-2xl px-4',
	attachment: 'size-15 rounded-full'
};

/** Glyph size per silhouette. A hub and an attachment carry a smaller icon. */
export function glyphClass(shape: NodeShape): string {
	return shape === 'trigger' || shape === 'step' ? 'size-7' : 'size-5';
}

/** Even spacing for `count` ports along one edge of a tile, as a percentage. */
export function portOffset(index: number, count: number): string {
	return `${((index + 1) / (count + 1)) * 100}%`;
}

/**
 * How one node is drawn, entirely from what the server declared.
 *
 * Nothing here keys on a node type. A type the editor has never seen arrives
 * with its own glyph or artwork, its own accent and its own subtitle, and needs
 * no frontend change — which is the whole point, because a generated pack of a
 * hundred operations is not going to get hand-written entries.
 */
export function nodeVisual(definition: Definition): NodeVisual {
	return {
		icon: builtinGlyph(definition),
		iconURL: servedIconURL(definition),
		accent: definition.iconColor || 'var(--muted-foreground)',
		shape: nodeShape(definition)
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

export function nodeShape(definition: Definition): NodeShape {
	const inputs = definition.inputs ?? [];
	const outputs = definition.outputs ?? [];

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

export function mainPorts(ports: Port[] | null | undefined): Port[] {
	return (ports ?? []).filter((port) => !isAttachment(port));
}

export function attachmentPorts(ports: Port[] | null | undefined): Port[] {
	return (ports ?? []).filter(isAttachment);
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

	const trimmed = rendered.replace(/\s+/g, ' ').trim();
	return trimmed === '' || trimmed === '/' ? null : trimmed;
}
