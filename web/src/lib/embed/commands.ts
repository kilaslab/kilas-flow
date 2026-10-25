/**
 * Host → editor commands, embed protocol v2.
 *
 * The frame half only parses and shapes; embed-editor.svelte owns what a
 * command actually does, because that is where the editor instance, the
 * scopes and the failure banners live. Parsing is strict — a message that is
 * not a well-formed command of ours is ignored outright, so a host page's
 * stray postMessage never invents a reply — but a *recognized* request with a
 * command the frame does not carry is answered, because the host is waiting
 * on a promise for exactly that request id.
 */

export const COMMAND_TYPE = 'kilasflow:command';
export const COMMAND_RESULT_TYPE = 'kilasflow:command-result';

export type HostCommand =
	| { command: 'save'; requestId: string }
	| { command: 'run'; requestId: string }
	| { command: 'focusNode'; requestId: string; nodeId: string };

/** A well-formed request naming a command this frame does not carry. */
export type UnknownCommand = { command: 'unknown'; requestId: string; name: string };

export type ParsedCommand = HostCommand | UnknownCommand;

/** Why a command was refused, as the host's `code` reads it. */
export type CommandFailureCode =
	| 'unknown-command'
	| 'not-ready'
	| 'forbidden'
	| 'unknown-node'
	| 'save-failed'
	| 'conflict'
	| 'run-failed';

export type CommandReply =
	| { requestId: string; ok: true; saved?: boolean; dirty?: boolean; revision?: number; executionId?: string | null; status?: string }
	| { requestId: string; ok: false; code: CommandFailureCode; message?: string; dirty?: boolean; executionId?: string | null };

/**
 * Reads one host command off a message's data.
 *
 * Returns `null` for anything that is not ours to answer: a message of
 * another type is another protocol's business, and a missing or empty request
 * id has no promise to resolve.
 */
export function parseHostCommand(data: unknown): ParsedCommand | null {
	if (!data || typeof data !== 'object') return null;
	const raw = data as Record<string, unknown>;
	if (raw.type !== COMMAND_TYPE) return null;
	const requestId = typeof raw.requestId === 'string' ? raw.requestId : '';
	if (!requestId) return null;

	switch (raw.command) {
		case 'save':
		case 'run':
			return { command: raw.command, requestId };
		case 'focusNode':
			return typeof raw.nodeId === 'string' && raw.nodeId !== '' ? { command: 'focusNode', requestId, nodeId: raw.nodeId } : null;
		default:
			return { command: 'unknown', requestId, name: typeof raw.command === 'string' ? raw.command : '' };
	}
}
