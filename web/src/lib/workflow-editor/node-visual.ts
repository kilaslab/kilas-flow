import type { Component } from 'svelte';

import Archive from '@lucide/svelte/icons/archive';
import Bot from '@lucide/svelte/icons/bot';
import Box from '@lucide/svelte/icons/box';
import Clock from '@lucide/svelte/icons/clock';
import Code from '@lucide/svelte/icons/code';
import CornerDownLeft from '@lucide/svelte/icons/corner-down-left';
import Database from '@lucide/svelte/icons/database';
import Globe from '@lucide/svelte/icons/globe';
import Merge from '@lucide/svelte/icons/merge';
import MousePointerClick from '@lucide/svelte/icons/mouse-pointer-click';
import PencilLine from '@lucide/svelte/icons/pencil-line';
import Sparkles from '@lucide/svelte/icons/sparkles';
import Split from '@lucide/svelte/icons/split';
import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
import Webhook from '@lucide/svelte/icons/webhook';
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
	/** A CSS custom property reference, applied as `--node-accent` on the node root. */
	accent: string;
	shape: NodeShape;
};

const ICONS: Record<string, Component> = {
	'kilasflow.manual': MousePointerClick,
	'kilasflow.webhook': Webhook,
	'kilasflow.schedule': Clock,
	'kilasflow.set': PencilLine,
	'kilasflow.if': Split,
	'kilasflow.merge': Merge,
	'kilasflow.httpRequest': Globe,
	'kilasflow.respondToWebhook': CornerDownLeft,
	'kilasflow.code': Code,
	'kilasflow.postgres': Database,
	'kilasflow.mysql': Database,
	'kilasflow.sqlite': Database,
	'kilasflow.agent': Bot,
	'kilasflow.chatModel': Sparkles,
	'kilasflow.memoryBuffer': Archive,
	'kilasflow.httpTool': Wrench,
	'kilasflow.unsupported': TriangleAlert
};

const ACCENTS: Record<string, string> = {
	Triggers: 'var(--node-trigger)',
	Core: 'var(--node-core)',
	AI: 'var(--node-ai)',
	Database: 'var(--node-data)',
	Imported: 'var(--node-imported)'
};

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

export function nodeVisual(definition: Definition): NodeVisual {
	return {
		icon: ICONS[definition.type] ?? Box,
		accent: ACCENTS[definition.category] ?? 'var(--muted-foreground)',
		shape: nodeShape(definition)
	};
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
	return port.Kind !== 'main';
}

export function mainPorts(ports: Port[] | null | undefined): Port[] {
	return (ports ?? []).filter((port) => !isAttachment(port));
}

export function attachmentPorts(ports: Port[] | null | undefined): Port[] {
	return (ports ?? []).filter(isAttachment);
}

/**
 * The one parameter worth reading without opening the node.
 *
 * A compact tile has room for a single line, so it goes to whatever most
 * distinguishes this node from another of the same type — the method and host
 * for a request, the schedule for a timer, the field count for an assignment.
 * Anything unset is omitted rather than padded with a placeholder: an empty
 * line is honest about a node that is not configured yet.
 */
export function nodeSubtitle(node: WorkflowNode, definition: Definition): string | null {
	const parameters = node.parameters ?? {};

	switch (definition.type) {
		case 'kilasflow.httpRequest':
		case 'kilasflow.httpTool': {
			const method = text(parameters.method) || 'GET';
			const url = text(parameters.url);
			return url ? `${method} ${host(url)}` : method;
		}
		case 'kilasflow.webhook': {
			const method = text(parameters.httpMethod) || 'POST';
			const path = text(parameters.path);
			return path ? `${method} /${path.replace(/^\/+/, '')}` : method;
		}
		case 'kilasflow.schedule':
			return text(parameters.cron) || null;
		case 'kilasflow.respondToWebhook':
			return text(parameters.responseCode) || '200';
		case 'kilasflow.set': {
			const count = Object.keys(record(parameters.assignments)).length;
			return count === 0 ? null : count === 1 ? '1 field' : `${count} fields`;
		}
		case 'kilasflow.if': {
			const condition = Array.isArray(parameters.conditions) ? parameters.conditions[0] : undefined;
			const field = isRecord(condition) ? text(condition.field) : '';
			return field || null;
		}
		case 'kilasflow.merge':
			return text(parameters.mode) || null;
		case 'kilasflow.postgres':
		case 'kilasflow.mysql':
		case 'kilasflow.sqlite':
			return text(parameters.operation) || null;
		case 'kilasflow.chatModel':
			return text(parameters.model) || null;
		case 'kilasflow.unsupported':
			return text(parameters.originalType) || null;
		default:
			return null;
	}
}

/** Hosts read better than full URLs at tile width, and an expression is shown as itself. */
function host(url: string): string {
	if (url.includes('{{')) return url;
	try {
		return new URL(url).host;
	} catch {
		return url;
	}
}

function text(value: unknown): string {
	if (typeof value === 'string') return value;
	if (typeof value === 'number') return String(value);
	// An expression has no resolved value here — the server owns that — so the
	// template is shown verbatim rather than a stale or invented result.
	if (isExpression(value)) return value.value;
	return '';
}

function record(value: unknown): Record<string, unknown> {
	return isRecord(value) ? value : {};
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return value !== null && typeof value === 'object' && !Array.isArray(value);
}
