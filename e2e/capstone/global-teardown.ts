// The capstone's cleanup, run once after the suite whatever its result.
//
// Every container the run started carries `kilasflow.capstone.run=<runId>` and
// so does its network, so removal is a label query rather than a list of names:
// a container the suite forgot to close is still removed, and an unrelated
// container on the same machine is never touched.
//
// KILASFLOW_CAPSTONE_KEEP=1 is the deliberate exception: an operator debugging
// a failed run wants to look at the container that failed, and the report says
// the run left containers behind. `teardownRun()` is exported from
// e2e/fixtures/epic-image.ts for the manual cleanup that follows.
import { readCapstoneConfig } from '../fixtures/epic-config';
import { teardownRun } from '../fixtures/epic-image';

export default async function globalTeardown(): Promise<void> {
	const config = readCapstoneConfig();
	if (config.keep) {
		// eslint-disable-next-line no-console
		console.log(
			`KILASFLOW_CAPSTONE_KEEP=1: leaving run ${config.runId}'s containers and network in place; ` +
				`remove them with \`docker ps -a --filter label=kilasflow.capstone.run=${config.runId}\``
		);
		return;
	}
	await teardownRun(config.runId);
}
