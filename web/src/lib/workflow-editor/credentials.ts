/**
 * Which credential types a node type can authenticate with.
 *
 * This is a presentation-side hint only: the server re-checks the type and the
 * credential's host scope before a secret is applied, so a client that sends
 * an unsupported pairing is rejected at execution rather than trusted.
 */
const BY_NODE_TYPE: Record<string, string[]> = {
	'kilasflow.httpRequest': ['httpBasicAuth', 'httpHeaderAuth', 'httpBearerAuth']
};

export function credentialTypesFor(nodeType: string): string[] {
	return BY_NODE_TYPE[nodeType] ?? [];
}
