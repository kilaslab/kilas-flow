import { defineConfig } from 'vitest/config';

export default defineConfig({
	test: {
		globals: true,
		// The default is Node, because the server client is Node-only. The
		// browser tests opt into jsdom with a per-file `@vitest-environment`
		// pragma, which is how Vitest 4 selects an environment per file.
		environment: 'node'
	}
});
