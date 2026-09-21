import type { ActivationNotice, Node } from '$lib/api/generated/models';
import { ApiError, message } from '$lib/api/http';
import * as m from '$lib/paraglide/messages.js';

/**
 * Reading the activation answer.
 *
 * `POST /workflows/{id}/activate` returns the workflow plus a `notices` array,
 * each naming a node and something activation could not do on the user's
 * behalf — most often "add this URL to the session's webhook configuration",
 * because registering a webhook writes to somebody else's instance and is off
 * by default.
 *
 * A notice is neither an error nor a log line. The workflow *is* active, and
 * the failure it guards against is silent: a trigger whose service was never
 * told where to deliver looks exactly like one that is listening, and nobody
 * finds out until a message goes unanswered. That is also why none of this
 * belongs in a toast — a notice that disappears after four seconds is the
 * same as no notice.
 *
 * The parsing lives here rather than in the toolbar for the reason the
 * validation helper's does: it is a reading of what the server said, and a
 * component that also owns that reading grows a second, slightly different
 * copy of it for every surface that activates a workflow.
 */

export type ActivationNoticeView = {
	/**
	 * Identifies the notice while the user dismisses others beside it. The
	 * index is part of it because one node can raise more than one notice, and
	 * keying on the node alone would make dismissing either dismiss both.
	 */
	key: string;
	nodeID: string;
	/** The node's name on the canvas, so the notice points at something the user can see. */
	nodeName: string;
	nodeType: string;
	message: string;
	/** The URL to paste elsewhere, when the notice names one. */
	url: string | null;
};

/**
 * Turns the activation response's notices into rows the UI can render.
 *
 * `nodes` is the document the workflow activated with. The notice carries a
 * node ID because that is what the server knows; the user picked a name, and
 * "trigger" means nothing to somebody looking at a canvas that says "WhatsApp
 * message received".
 */
export function activationNotices(
	notices: ActivationNotice[] | null | undefined,
	nodes: Node[] | null | undefined
): ActivationNoticeView[] {
	if (!notices) return [];
	const namesByID = new Map((nodes ?? []).map((node) => [node.id, node.name]));

	return notices.map((notice, index) => ({
		key: `${index}-${notice.nodeId}`,
		nodeID: notice.nodeId,
		nodeName: namesByID.get(notice.nodeId) || notice.nodeId || notice.nodeType,
		nodeType: notice.nodeType,
		message: notice.message,
		url: copyableURL(notice.message)
	}));
}

/** Drops one notice, leaving every other notice in the same answer standing. */
export function dismissNotice(notices: ActivationNoticeView[], key: string): ActivationNoticeView[] {
	return notices.filter((notice) => notice.key !== key);
}

// A notice states its URL inside a sentence, because the same text has to read
// as prose in the API answer and in a support thread. Pulling the URL back out
// is what lets the UI offer a copy button, and pasting it into somebody else's
// console is the entire point of the notice.
const urlInProse = /https?:\/\/[^\s<>"']+/;

// A URL that ends a sentence swallows the full stop, and one inside brackets
// swallows the closing bracket. Either produces a 404 in the console the user
// pastes it into, which is worse than offering no copy button at all.
const trailingPunctuation = /[.,;:!?)\]}'"]+$/;

/** The URL a notice tells the user to paste, or null when it names none. */
export function copyableURL(text: string): string | null {
	const match = urlInProse.exec(text);
	if (!match) return null;
	const url = match[0].replace(trailingPunctuation, '');
	// Nothing survives a match that was only punctuation, and a bare scheme is
	// not something anyone can paste anywhere.
	return /^https?:\/\/[^/]/.test(url) ? url : null;
}

/**
 * The sentence shown when activation fails rather than returning notices.
 *
 * A 502 is the one failure the server rolls back for the user: a trigger could
 * not register, half-registered is worse than inactive, and the workflow was
 * put back the way it was. Saying so is the difference between a failure the
 * user understands and a status code that reads like a transient blip — and it
 * is what keeps the failure from being mistaken for a notice, which is a thing
 * you can act on later.
 */
export function activationFailure(error: unknown): string {
	if (error instanceof ApiError && error.status === 502) {
		return m.editor_activation_trigger_failed({ error: message(error) });
	}
	return message(error);
}
