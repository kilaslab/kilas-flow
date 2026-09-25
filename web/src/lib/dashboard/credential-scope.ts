import type { CredentialTypeResource } from '$lib/api/generated/models';

/**
 * What an empty allowed-hosts list means for one credential type.
 *
 * It is not always "any host". A type whose service lives at one address
 * (OpenAI, OpenRouter, Google) is confined to it, and a type that names its own
 * server (a Telegram Bot API base URL, a WAHA instance) is confined to that
 * server's host. The form says which, so leaving the field empty is a choice
 * the author can read rather than a guess.
 */
export type DefaultScope =
	| { kind: 'any' }
	| { kind: 'hosts'; hosts: string[] }
	| { kind: 'derived'; field: string };

type ScopedDefinition = Pick<CredentialTypeResource, 'defaultDomains' | 'defaultDomainsFrom' | 'fields'>;

export function defaultScope(definition: ScopedDefinition | null | undefined): DefaultScope {
	if (!definition) return { kind: 'any' };
	const hosts = (definition.defaultDomains ?? []).filter((host) => host.trim() !== '');
	if (hosts.length > 0) return { kind: 'hosts', hosts };
	const key = definition.defaultDomainsFrom?.trim();
	if (key) {
		// The field's own label, so the hint names what the author sees on
		// the form rather than a payload key.
		const label = (definition.fields ?? []).find((field) => field.key === key)?.label ?? key;
		return { kind: 'derived', field: label };
	}
	return { kind: 'any' };
}
