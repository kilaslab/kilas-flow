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
export function credentialTypesFor(definition: Definition | undefined): string[] {
	return (definition?.credentials ?? []).map((requirement) => requirement.type);
}

/** Whether a node cannot run without a credential attached. */
export function requiresCredential(definition: Definition | undefined): boolean {
	return (definition?.credentials ?? []).some((requirement) => requirement.required);
}
