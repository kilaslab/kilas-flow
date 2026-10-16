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

/** Asks the server about each id on its own, so one failure cannot taint another. */
export async function probeCredentials(
	ids: readonly string[],
	read: (id: string) => Promise<unknown> = getCredential
): Promise<Record<string, CredentialPresence>> {
	const verdicts = await Promise.all(
		ids.map(async (id): Promise<[string, CredentialPresence]> => {
			try {
				await read(id);
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
