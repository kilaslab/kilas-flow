import type { Node, WebhookDeclaration, WebhookRouteResource } from '$lib/api/generated/models';

/**
 * What the properties panel says about a webhook node's public address.
 *
 * The address is never derived from the path the user typed. The live route is
 * an opaque one minted by the server and reused for the node's lifetime, so a
 * `/webhook/<path>` built on the client answers 404 however carefully it is
 * spelled — which is what the panel used to show. The only source of the
 * address is `GET /workflows/{id}/webhooks`, and this module decides which of
 * its answers the panel can honestly display.
 */
export type WebhookAddress =
	/** The node has no path on the canvas, so there is nothing to bind. */
	| { kind: 'needs-path' }
	/** A path is set but the saved revision does not carry it, so no route exists to ask about. */
	| { kind: 'needs-save' }
	| { kind: 'loading' }
	/** The lookup failed. Not fatal: the rest of the panel is unaffected. */
	| { kind: 'unavailable' }
	/** The lookup succeeded and the server binds nothing to this node. */
	| { kind: 'unbound' }
	/** `live` is whether the workflow is active, which is when the address starts answering. */
	| { kind: 'ready'; url: string; live: boolean };

/**
 * The path a webhook node listens on, as the node holds it.
 *
 * Mirrors the server's extractor: the declared parameter wins when the node
 * carries a string for it, and the declaration's fixed path answers otherwise.
 */
export function webhookPathOf(declaration: WebhookDeclaration, node: Node | null | undefined): string {
	const configured = declaration.pathParameter ? node?.parameters?.[declaration.pathParameter] : undefined;
	return (typeof configured === 'string' ? configured : (declaration.staticPath ?? '')).trim();
}

/**
 * Whether the saved revision gives this node a route to look up.
 *
 * The server binds a trigger only when it is saved, enabled and has a path.
 * Asking for a node that fails any of these can only come back empty, and
 * doing it anyway would blame the answer for a state the user can fix.
 */
export function savedNodeBinds(declaration: WebhookDeclaration, saved: Node | null | undefined): boolean {
	return Boolean(saved) && !saved?.disabled && webhookPathOf(declaration, saved) !== '';
}

/**
 * Joins the server's route to the host the browser reached it at.
 *
 * The API answers `/webhook/<route>` with no host, because the server cannot
 * know which of its names the sender will use. The origin the editor itself
 * was served from is the one address known to reach this instance, and
 * production serves the API, the editor and the webhook surface from it.
 */
export function absoluteWebhookURL(url: string, origin: string): string {
	if (/^https?:\/\//i.test(url)) return url;
	return `${origin.replace(/\/+$/, '')}/${url.replace(/^\/+/, '')}`;
}

export type WebhookAddressInput = {
	declaration: WebhookDeclaration;
	/** The node on the canvas, saved or not. */
	node: Node;
	/** The node as the last saved revision holds it; absent when that revision lacks it. */
	saved: Node | null | undefined;
	/** The workflow's bindings; undefined until the lookup answers. */
	bindings: WebhookRouteResource[] | undefined;
	failed: boolean;
	origin: string;
	active: boolean;
};

export function webhookAddress(input: WebhookAddressInput): WebhookAddress {
	if (webhookPathOf(input.declaration, input.node) === '') return { kind: 'needs-path' };
	if (!savedNodeBinds(input.declaration, input.saved)) return { kind: 'needs-save' };
	if (input.failed) return { kind: 'unavailable' };
	if (!input.bindings) return { kind: 'loading' };
	const binding = input.bindings.find((candidate) => candidate.nodeId === input.node.id);
	if (!binding) return { kind: 'unbound' };
	return { kind: 'ready', url: absoluteWebhookURL(binding.url, input.origin), live: input.active };
}
