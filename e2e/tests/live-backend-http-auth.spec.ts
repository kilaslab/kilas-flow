// Outbound credential placement suite (EPIC-87t47t): the generic HTTP
// credential family this server offers, proven end to end against the real
// binary.
//
// The observable is the request the stub received, never the node's
// configuration: a credential that is stored correctly and never applied is
// exactly the failure this suite exists to catch. The stub records the raw
// query string and the headers the workflow actually sent
// (e2e/helpers/stub.ts), so both placements are read from the wire.
import { test, expect } from '../fixtures';
import { createCredential, createWorkflow, runWorkflow, waitForExecution } from '../helpers/seed';
import { uniqueName } from '../fixtures/datastore';

type JsonObject = Record<string, unknown>;

// httpAuthDocument is the smallest workflow that exercises one credential: a
// manual trigger driving a GET against the stub, with the credential attached
// to the HTTP node. The seed helper's httpWorkflowDocument has no credential
// slot, and the credential is the whole point of these tests.
function httpAuthDocument(name: string, url: string, credentialType: string, credentialId: string): JsonObject {
	return {
		schemaVersion: 1,
		name,
		nodes: [
			{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
			{
				id: 'http',
				name: 'Call stub',
				type: 'kilasflow.httpRequest',
				typeVersion: 1,
				position: { x: 240, y: 0 },
				parameters: { method: 'GET', url },
				credentials: { [credentialType]: credentialId }
			}
		],
		connections: [
			{
				id: 'c1',
				kind: 'main',
				source: { nodeId: 'manual', port: 'main' },
				target: { nodeId: 'http', port: 'main' }
			}
		],
		settings: {}
	};
}

test('query auth reaches the API key in the query string', async ({ server, stub }) => {
	const baseURL = server.baseURL;
	const credential = await createCredential(baseURL, {
		name: uniqueName('live query auth'),
		type: 'httpQueryAuth',
		fields: { name: 'api_key', value: 'k-1' }
	});
	const workflow = await createWorkflow(
		baseURL,
		httpAuthDocument(uniqueName('live query auth'), stub.url('/echo'), 'httpQueryAuth', credential.id)
	);

	const executionId = await runWorkflow(baseURL, workflow.id);
	const record = await waitForExecution(baseURL, executionId);
	expect(record.status).toBe('succeeded');

	// The node completed before the run did, so the stub has already recorded
	// the request: reading it needs no extra poll.
	const request = stub.requests[0];
	expect(request, `the workflow never reached the stub (execution ${executionId})`).toBeTruthy();
	expect(request.path).toBe('/echo');
	expect(request.query).toBe('?api_key=k-1');
});

test('custom auth sends every header and query parameter in its template', async ({ server, stub }) => {
	const baseURL = server.baseURL;
	const credential = await createCredential(baseURL, {
		name: uniqueName('live custom auth'),
		type: 'httpCustomAuth',
		fields: { json: JSON.stringify({ headers: { 'x-api-key': 'k-2' }, qs: { tenant: 'acme' } }) }
	});
	const workflow = await createWorkflow(
		baseURL,
		httpAuthDocument(uniqueName('live custom auth'), stub.url('/echo'), 'httpCustomAuth', credential.id)
	);

	const executionId = await runWorkflow(baseURL, workflow.id);
	const record = await waitForExecution(baseURL, executionId);
	expect(record.status).toBe('succeeded');

	const request = stub.requests[0];
	expect(request, `the workflow never reached the stub (execution ${executionId})`).toBeTruthy();
	// One credential injects both places, which is what n8n's Custom Auth
	// template means and what no single-field credential can express.
	expect(request.headers['x-api-key']).toBe('k-2');
	expect(request.query).toContain('tenant=acme');
});
