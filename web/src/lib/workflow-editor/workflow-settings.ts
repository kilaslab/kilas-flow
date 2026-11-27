import type { Document } from '$lib/api/generated/models';

/** The longest workflow name the API accepts; the list's rename enforces the same bound. */
export const MAX_WORKFLOW_NAME_LENGTH = 255;

/**
 * The document settings the editor offers, under the names the Go engine
 * reads (`engine.ExecutionTimeoutSetting`, `engine.errorWorkflowSetting` and
 * the compiler's timezone check). Any other key a document carries is left
 * exactly as it was found.
 */
export const EXECUTION_TIMEOUT_KEY = 'executionTimeout';
export const ERROR_WORKFLOW_KEY = 'errorWorkflow';
export const TIMEZONE_KEY = 'timezone';

/** n8n's own spelling of "no execution timeout", and the engine's. */
const NO_TIMEOUT = -1;

/** The settings dialog's fields as the form holds them. */
export interface WorkflowSettingsForm {
	/** Seconds as typed; empty means the instance default applies. Ignored while `noTimeout` is set. */
	timeoutSeconds: string;
	/** Run without a time limit (`executionTimeout: -1`). */
	noTimeout: boolean;
	/** The id of the workflow started when this one fails; empty for none. */
	errorWorkflow: string;
	/** An IANA zone name; empty means the server's default zone. */
	timezone: string;
}

export type SettingsProblem = 'timeout';

/** Reads the form's starting values from a document's settings. */
export function readSettingsForm(document: Document): WorkflowSettingsForm {
	const settings = document.settings ?? {};
	const timeout = settings[EXECUTION_TIMEOUT_KEY];
	const errorWorkflow = settings[ERROR_WORKFLOW_KEY];
	const timezone = settings[TIMEZONE_KEY];
	return {
		timeoutSeconds: typeof timeout === 'number' && timeout > 0 ? String(timeout) : '',
		noTimeout: typeof timeout === 'number' && timeout < 0,
		errorWorkflow: typeof errorWorkflow === 'string' ? errorWorkflow.trim() : '',
		// n8n's "DEFAULT" sentinel means the server's zone, which is what empty says here.
		timezone: typeof timezone === 'string' && timezone.trim().toUpperCase() !== 'DEFAULT' ? timezone.trim() : ''
	};
}

/**
 * Writes the form into the document's settings.
 *
 * An empty field removes its key rather than storing an empty value, so a
 * workflow that sets nothing stays byte-identical to one saved before this
 * dialog existed. The same document comes back when nothing changed, so a
 * dialog that was opened and saved untouched does not dirty the draft.
 */
export function applySettingsForm(document: Document, form: WorkflowSettingsForm): { document: Document } | { problem: SettingsProblem } {
	const next: Record<string, unknown> = { ...(document.settings ?? {}) };
	delete next[EXECUTION_TIMEOUT_KEY];
	delete next[ERROR_WORKFLOW_KEY];
	delete next[TIMEZONE_KEY];

	if (form.noTimeout) {
		next[EXECUTION_TIMEOUT_KEY] = NO_TIMEOUT;
	} else if (form.timeoutSeconds.trim() !== '') {
		const seconds = Number(form.timeoutSeconds.trim());
		if (!Number.isFinite(seconds) || seconds <= 0) return { problem: 'timeout' };
		next[EXECUTION_TIMEOUT_KEY] = seconds;
	}
	const errorWorkflow = form.errorWorkflow.trim();
	if (errorWorkflow !== '') next[ERROR_WORKFLOW_KEY] = errorWorkflow;
	const timezone = form.timezone.trim();
	if (timezone !== '') next[TIMEZONE_KEY] = timezone;

	if (sameSettings(document.settings ?? {}, next)) return { document };
	return { document: { ...document, settings: next } };
}

function sameSettings(left: Record<string, unknown>, right: Record<string, unknown>): boolean {
	const keys = new Set([...Object.keys(left), ...Object.keys(right)]);
	for (const key of keys) {
		if (JSON.stringify(left[key]) !== JSON.stringify(right[key])) return false;
	}
	return true;
}

export type RenameProblem = 'required' | 'too_long';

/** Renames the draft; the same document comes back when the name did not change. */
export function renameWorkflow(document: Document, raw: string): { document: Document } | { problem: RenameProblem } {
	const name = raw.trim();
	if (name === '') return { problem: 'required' };
	if (name.length > MAX_WORKFLOW_NAME_LENGTH) return { problem: 'too_long' };
	if (name === document.name) return { document };
	return { document: { ...document, name } };
}

/**
 * The zones a select offers: what the runtime lists, plus UTC (which some
 * runtimes leave out) and the value already stored (so a workflow that names a
 * zone this browser does not list still shows it instead of silently picking
 * another).
 */
export function timezoneOptions(supported: readonly string[], current: string): string[] {
	const zones = new Set(supported);
	zones.add('UTC');
	if (current !== '') zones.add(current);
	return [...zones].sort((left, right) => left.localeCompare(right));
}

export interface ErrorWorkflowOption {
	id: string;
	name: string;
	/** The stored id names a workflow the tenant no longer lists. */
	missing: boolean;
}

/**
 * The workflows an error workflow can be chosen from: every other workflow,
 * sorted by name. The engine refuses a workflow that names itself, so the
 * current one is never offered. A stored id that is not in the list is kept as
 * a "missing" row so the dialog shows what the document holds.
 */
export function errorWorkflowOptions(workflows: readonly { id: string; name: string }[], currentID: string, selected: string): ErrorWorkflowOption[] {
	const options: ErrorWorkflowOption[] = workflows
		.filter((workflow) => workflow.id !== currentID)
		.map((workflow) => ({ id: workflow.id, name: workflow.name, missing: false }))
		.sort((left, right) => left.name.localeCompare(right.name));
	if (selected !== '' && !options.some((option) => option.id === selected)) {
		options.unshift({ id: selected, name: selected, missing: true });
	}
	return options;
}
