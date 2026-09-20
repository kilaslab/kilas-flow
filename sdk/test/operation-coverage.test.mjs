/**
 * Operation coverage gate.
 *
 * This file is JavaScript on purpose: the SDK typechecks under
 * `moduleResolution: bundler`, which leaves `node:` imports unresolved, and
 * this gate shells out to a helper script and reads files. The mock-transport
 * behaviour tests stay in TypeScript next to it.
 */
import { execFile } from 'node:child_process';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { KilasFlowClient } from '../src/server.ts';

const sdkDir = join(dirname(fileURLToPath(import.meta.url)), '..');

/**
 * The operation surface, from docs/src/content/docs/reference/api-contract.md:
 * every operation id the API declares, and the client method that covers it.
 *
 * Two operations stream rather than answer JSON, so they are covered by a URL
 * builder on the KilasFlowClient instead of a request method — the same
 * precedent `executionEventsUrl` set: a host rendering an icon or opening an
 * event stream wants a URL, not bytes forced through a JSON transport.
 */
const OPERATION_COVERAGE = {
	'create-workflow': 'createWorkflow',
	'list-workflows': 'listWorkflows',
	'get-workflow': 'getWorkflow',
	'update-workflow': 'updateWorkflow',
	'delete-workflow': 'deleteWorkflow',
	'run-workflow': 'runWorkflow',
	'activate-workflow': 'activateWorkflow',
	'deactivate-workflow': 'deactivateWorkflow',
	'list-workflow-versions': 'listWorkflowVersions',
	'get-workflow-version': 'getWorkflowVersion',
	'publish-workflow-version': 'publishWorkflowVersion',
	'restore-workflow-version': 'restoreWorkflowVersion',
	'list-workflow-publish-events': 'listWorkflowPublishEvents',
	'list-workflow-webhooks': 'listWorkflowWebhooks',
	'workflow-diagnostics': 'getWorkflowDiagnostics',
	'list-executions': 'listExecutions',
	'get-execution': 'getExecution',
	'cancel-execution': 'cancelExecution',
	'stream-execution-events': 'executionEventsUrl',
	'list-credential-types': 'listCredentialTypes',
	'test-credential-payload': 'testCredentialPayload',
	'list-credentials': 'listCredentials',
	'create-credential': 'createCredential',
	'get-credential': 'getCredential',
	'update-credential': 'updateCredential',
	'delete-credential': 'deleteCredential',
	'test-credential': 'testCredential',
	login: 'login',
	logout: 'logout',
	'get-me': 'getMe',
	'list-api-keys': 'listApiKeys',
	'create-api-key': 'createApiKey',
	'revoke-api-key': 'revokeApiKey',
	'create-stream-ticket': 'createStreamTicket',
	'list-schedules': 'listSchedules',
	'create-schedule': 'createSchedule',
	'update-schedule': 'updateSchedule',
	'delete-schedule': 'deleteSchedule',
	'list-node-types': 'listNodeTypes',
	'get-node-icon': 'nodeIconUrl',
	'load-node-property-options': 'loadNodePropertyOptions',
	'load-node-property-schema': 'loadNodePropertySchema',
	'get-expression-grammar': 'getExpressionGrammar',
	'import-workflow': 'importWorkflow',
	'export-workflow': 'exportWorkflow',
	'create-embed-session': 'createEmbedSession',
	// The embed-scoped datastore subset (FEAT-1c70nt): the definition and row
	// reads a datastore session may reach, plus the single-row insert proving
	// the write scope. The management surface — DDL, columns, filtered
	// writes, transfer — is covered below (FEAT-nc6z9r), completing what the
	// subset starts.
	'get-datastore': 'getDatastore',
	'list-datastore-rows': 'listDatastoreRows',
	'get-datastore-row': 'getDatastoreRow',
	'insert-datastore-row': 'insertDatastoreRow',
	'list-datastores': 'listDatastores',
	'create-datastore': 'createDatastore',
	'rename-datastore': 'renameDatastore',
	'delete-datastore': 'deleteDatastore',
	'clear-datastore': 'clearDatastore',
	'add-datastore-column': 'addDatastoreColumn',
	'rename-datastore-column': 'renameDatastoreColumn',
	'delete-datastore-column': 'deleteDatastoreColumn',
	'update-datastore-rows': 'updateDatastoreRows',
	'delete-datastore-rows': 'deleteDatastoreRows',
	'upsert-datastore-row': 'upsertDatastoreRow',
	'export-datastore-rows': 'exportDatastoreRows',
	'import-datastore-rows': 'importDatastoreRows',
	'get-health': 'getHealth',
	'get-ready': 'getReady',
	// The operator surface: tenants, their accounts, and keys minted for
	// another tenant. The server refuses anything but an operator credential,
	// but every operation is still reachable through a client method.
	'list-tenants': 'listTenants',
	'create-tenant': 'createTenant',
	'get-tenant': 'getTenant',
	'create-tenant-api-key': 'createTenantApiKey',
	'list-tenant-users': 'listTenantUsers',
	'create-tenant-user': 'createTenantUser',
	'disable-tenant-user': 'disableTenantUser',
	'enable-tenant-user': 'enableTenantUser',
	'set-tenant-user-password': 'setTenantUserPassword'

};

/**
 * Operations with no client method, and why. Empty today: every declared
 * operation is reachable. An entry here must justify itself — "not yet" is
 * not a reason — and the gate below fails on an exclusion without one.
 */
const EXCLUSIONS = {};

const execFileAsync = promisify(execFile);

async function liveOperationIds() {
	const workspace = await mkdtemp(join(tmpdir(), 'kilasflow-sdk-coverage-'));
	try {
		const specPath = join(workspace, 'openapi.json');
		await execFileAsync(process.execPath, ['scripts/dump-openapi.mjs', specPath], {
			cwd: sdkDir,
			timeout: 300_000
		});
		const document = JSON.parse(await readFile(specPath, 'utf8'));
		return Object.values(document.paths).flatMap((pathItem) =>
			Object.values(pathItem)
				.map((operation) => operation.operationId)
				.filter((id) => typeof id === 'string')
		);
	} finally {
		await rm(workspace, { recursive: true, force: true });
	}
}

describe('operation coverage', () => {
	it('names a real client method for every covered operation', () => {
		for (const [operation, method] of Object.entries(OPERATION_COVERAGE)) {
			expect(
				typeof KilasFlowClient.prototype[method],
				`${operation} names ${method}, which does not exist on KilasFlowClient`
			).toBe('function');
		}
	});

	/**
	 * `sdk/README.md` is baked into the immutable tarball, so a total or a
	 * missing method name there cannot be corrected for a published version:
	 * the count and the method list are asserted against the coverage map,
	 * which the test above ties to the live document.
	 */
	it('states the same surface in the shipped README', async () => {
		const readme = await readFile(join(sdkDir, 'README.md'), 'utf8');
		const methods = Object.values(OPERATION_COVERAGE);
		const unnamed = methods.filter((method) => !readme.includes(`\`${method}\``));
		expect(unnamed, `methods the shipped README never names: ${unnamed.join(', ')}`).toEqual([]);

		const stated = readme.match(/all\s+(\d+)\s+under `\/api\/v1`/);
		expect(stated, 'the README no longer states how many operations the surface covers').not.toBeNull();
		expect(Number(stated[1]), 'the README states a different total than the coverage map').toBe(
			Object.keys(OPERATION_COVERAGE).length
		);
	});

	// Boots a real binary, so this is the slowest test in the package by far.
	// That is the point: the document comes from the server, never from a
	// checked-in copy, so a new handler without a client method fails here.
	it(
		'covers every operation the server declares',
		{ timeout: 300_000 },
		async () => {
			const operationIds = await liveOperationIds();

			const missing = operationIds.filter((id) => !(id in OPERATION_COVERAGE) && !(id in EXCLUSIONS));
			expect(missing, `operations with no client method and no exclusion: ${missing.join(', ')}`).toEqual([]);

			const stale = Object.keys(OPERATION_COVERAGE).filter((id) => !operationIds.includes(id));
			expect(stale, `covered operations the server no longer declares: ${stale.join(', ')}`).toEqual([]);

			const unjustified = Object.entries(EXCLUSIONS).filter(([, reason]) => !reason.trim());
			expect(unjustified, 'exclusions without a stated reason').toEqual([]);
		}
	);
});
