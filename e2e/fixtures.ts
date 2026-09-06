import { copyFile } from 'node:fs/promises';
import { test as base } from '@playwright/test';

import { startServer, type E2EServer } from './helpers/server';
import { startStub, type StubServer } from './helpers/stub';

interface E2EFixtures {
	stub: StubServer;
	server: E2EServer;
}

// One fresh instance per test: its own stub, its own kilasflow (own port,
// own data directory, own workflows). Stronger than per-file isolation, so
// parallel workers and reruns cannot interfere through the database or the
// scheduler. The stub boots first because the server's outbound policy names
// the stub's endpoint.
export const test = base.extend<E2EFixtures>({
	stub: async ({}, use) => {
		const stub = await startStub();
		await use(stub);
		await stub.close();
	},
	server: async ({ stub }, use, testInfo) => {
		const server = await startServer({
			stubEndpoint: `127.0.0.1:${stub.port}`,
			allowedOrigin: stub.origin
		});
		await use(server);
		if (testInfo.status !== testInfo.expectedStatus) {
			// Keyed by instance port, alongside Playwright's own trace and
			// screenshot: a failure names the server that produced it.
			const snapshot = testInfo.outputPath(`kilasflow-${server.port}.log`);
			await copyFile(server.logPath, snapshot);
			await testInfo.attach('kilasflow-server-log', { path: snapshot, contentType: 'text/plain' });
		}
		await server.close();
	}
});

export { expect } from '@playwright/test';
