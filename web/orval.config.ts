import { defineConfig } from 'orval';

export default defineConfig({
	kilasflow: {
		input: {
			target: './.tmp/openapi.json'
		},
		output: {
			target: './src/lib/api/generated/index.ts',
			schemas: './src/lib/api/generated/models',
			client: 'svelte-query',
			httpClient: 'fetch',
			mode: 'tags-split',
			clean: true,
			override: {
				mutator: {
					path: './src/lib/api/http.ts',
					name: 'apiFetch'
				}
			}
		}
	}
});
