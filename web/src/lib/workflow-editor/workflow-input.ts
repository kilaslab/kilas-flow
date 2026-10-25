import type { Document, WorkflowDocumentInput } from '$lib/api/generated/models';

/**
 * The body of a draft save: the document without its server-owned identity.
 *
 * Every save goes through this one function (the editor, the embed editor, and
 * the list's rename and duplicate), so a field the document gains has exactly
 * one place to be added, and no save silently drops it. It lives in a module of
 * its own, away from the canvas helpers in document.ts, so the workflows list
 * can use it without loading the flow library.
 */
export function toWorkflowInput(document: Document): WorkflowDocumentInput {
	return {
		schemaVersion: document.schemaVersion,
		name: document.name,
		nodes: clone(document.nodes ?? []),
		connections: clone(document.connections ?? []),
		settings: clone(document.settings ?? {}),
		// A save replaces the stored tags with these, so they are always sent.
		tags: clone(document.tags ?? []),
		// Pinned data too — a save replaces what each pinned node replays —
		// but only when there is any: an empty pin map is no pin at all, and
		// sending one would make every previously-clean save body grow a field.
		...(document.pinData && Object.keys(document.pinData).length > 0 ? { pinData: clone(document.pinData) } : {})
	};
}

function clone<T>(value: T): T {
	if (value === undefined) return value;
	return JSON.parse(JSON.stringify(value)) as T;
}
