import { getCredential } from '$lib/api/generated/credentials/credentials';
import type { Definition } from '$lib/api/generated/models';
import { ApiError } from '$lib/api/http';

/**
 * Which credential types a node can authenticate with, read from the node's own
 * declaration.
 *
 * This used to be a map keyed by node type with `[]` as its default, and that
 * default was not cosmetic: the properties panel renders the credential picker
 * only when this returns something, so a node absent from the map got **no
 * credential selector at all** — not an empty one, not a disabled one, the
 * block simply did not render. A generated pack's node would have had no way to
 * attach the API key it needs.
 *
 * It remains a presentation-side hint: the server re-checks the type and the
 * credential's host scope before a secret is applied, so a client sending an
 * unsupported pairing is rejected at execution rather than trusted.
 */
export function credentialTypesFor(
	definition: Definition | undefined,
	parameters?: Record<string, unknown>
): string[] {
	const requirements = definition?.credentials ?? [];
	if (!parameters) return requirements.map((requirement) => requirement.type);
	return requirements.filter((requirement) => credentialRequirementVisible(requirement, parameters)).map((requirement) => requirement.type);
}

/**
 * Whether one credential requirement applies to the node's current
 * parameters. A requirement without a visibleWhen rule always applies; one
 * with entries follows the same OR-within-a-key, AND-across-keys rule as
 * property visibility, so the webhook panel does not offer basicAuth and
 * headerAuth while Authentication is None.
 */
function credentialRequirementVisible(
	requirement: { visibleWhen?: { key: string; equals: unknown }[] | null },
	parameters: Record<string, unknown>
): boolean {
	const shorthand = requirement.visibleWhen ?? [];
	if (shorthand.length === 0) return true;
	const merged: Record<string, unknown[]> = {};
	for (const condition of shorthand) {
		(merged[condition.key] ??= []).push(condition.equals);
	}
	return Object.entries(merged).every(([key, values]) => values.some((candidate) => parameters[key] === candidate));
}

/** Whether a node cannot run without a credential attached. */
export function requiresCredential(definition: Definition | undefined): boolean {
	return (definition?.credentials ?? []).some((requirement) => requirement.required);
}

/**
 * What the server says about one credential id a node names.
 *
 * `deleted` is the only verdict that flags anything, and it comes from one
 * source: GET /credentials/{id} answering 404. It is never inferred from the id
 * missing out of the credential list, because that list proves nothing about
 * absence: an embed session's listing is confined to the credentials its grant
 * names (and the endpoint itself is refused to it, 403), and the dashboard's
 * list can fail to load or hold only the first page. Anything but a plain 404
 * — a refusal, an outage, a dropped connection — is `unknown`.
 */
export type CredentialPresence = 'present' | 'deleted' | 'unknown';

/** Cache key for the verdicts on a set of ids, independent of their order. */
export function credentialPresenceKey(ids: readonly string[]): readonly string[] {
	return ['credential-presence', ...[...ids].sort()];
}

/**
 * The credential ids worth asking the server about: the ones the panel is
 * showing a picker for right now.
 *
 * An id counts only when its type is one the panel renders (a hidden type's
 * reference is not on screen, so nothing would display its verdict), only while
 * the credential section is shown, and only once the list has loaded (an empty
 * list before the load finishes would send every attached id to the server).
 * Ids the list holds are known to exist, and one id attached under two types is
 * asked about once. Each probe that finds a live credential makes the server
 * run its usage scan, so the set is kept as small as the UI it feeds.
 */
export function credentialIdsToProbe(input: {
	types: readonly string[];
	attached: Record<string, string> | undefined;
	listed: readonly { id: string }[];
	shown: boolean;
	listLoaded: boolean;
}): string[] {
	if (!input.shown || !input.listLoaded) return [];
	const known = new Set(input.listed.map((credential) => credential.id));
	const ids = new Set<string>();
	for (const type of input.types) {
		const id = input.attached?.[type];
		if (id && !known.has(id)) ids.add(id);
	}
	return [...ids];
}

/** Asks the server about each id on its own, so one failure cannot taint another. */
export async function probeCredentials(
	ids: readonly string[],
	read: (id: string, signal?: AbortSignal) => Promise<unknown> = (id, signal) => getCredential(id, { signal }),
	signal?: AbortSignal
): Promise<Record<string, CredentialPresence>> {
	const verdicts = await Promise.all(
		[...new Set(ids)].map(async (id): Promise<[string, CredentialPresence]> => {
			try {
				await read(id, signal);
				return [id, 'present'];
			} catch (error) {
				return [id, error instanceof ApiError && error.status === 404 ? 'deleted' : 'unknown'];
			}
		})
	);
	return Object.fromEntries(verdicts);
}

/** Whether the server's verdict on this id is that the credential is gone. */
export function credentialGone(verdicts: Record<string, CredentialPresence> | undefined, id: string): boolean {
	return verdicts?.[id] === 'deleted';
}
