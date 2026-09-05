/**
 * Which credential types a node type can authenticate with.
 *
 * This is a presentation-side hint only: the server re-checks the type and the
 * credential's host scope before a secret is applied, so a client that sends
 * an unsupported pairing is rejected at execution rather than trusted.
 */
const BY_NODE_TYPE: Record<string, string[]> = {
	'kilasflow.httpRequest': ['httpBasicAuth', 'httpHeaderAuth', 'httpBearerAuth'],
	// A webhook uses a credential to authenticate callers, not to call out, so
	// only the two modes the inbound boundary can verify are offered.
	'kilasflow.webhook': ['httpBasicAuth', 'httpHeaderAuth'],
	// Each database node accepts exactly its own driver's credential; there is
	// no shared or fallback connection to fall back to.
	'kilasflow.postgres': ['postgres'],
	'kilasflow.mysql': ['mysql'],
	'kilasflow.sqlite': ['sqlite']
};

export function credentialTypesFor(nodeType: string): string[] {
	return BY_NODE_TYPE[nodeType] ?? [];
}
