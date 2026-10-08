import type { CredentialTypeResource, TestCredentialResource } from '$lib/api/generated/models';

/**
 * What a credential test answered, as one of the three answers the server
 * gives.
 *
 * "Untestable" is not a failure: the server has no probe for the type, so it
 * says nothing about whether the credential works. Showing it as a red cross
 * told people a working credential was broken.
 */
export type TestVerdict = { kind: 'ok' | 'failed' | 'untestable'; detail: string };

export function testVerdict(result: Pick<TestCredentialResource, 'ok' | 'detail' | 'untestable'>): TestVerdict {
	const detail = result.detail ?? '';
	if (result.ok) return { kind: 'ok', detail };
	if (result.untestable) return { kind: 'untestable', detail };
	return { kind: 'failed', detail };
}

/**
 * Whether the editor offers Test for a type. The catalogue says which types the
 * server can probe, so a type with no probe gets no button rather than one that
 * cannot pass.
 */
export function canTest(definition: Pick<CredentialTypeResource, 'testable'> | null | undefined): boolean {
	return definition?.testable === true;
}
