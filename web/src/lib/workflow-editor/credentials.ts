import type { Definition } from '$lib/api/generated/models';

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
