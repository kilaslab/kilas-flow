import { defineConfig } from 'orval';

export default defineConfig({
	kilasflow: {
		input: {
			target: './.tmp/openapi.json'
		},
		output: {
			// Types only. The SDK writes its own request layer, but every shape
			// it exposes comes from the server's own OpenAPI document, so an
			// endpoint can never be defined in two places that disagree.
			target: './src/generated/models.ts',
			client: 'fetch',
			mode: 'single',
			clean: true
		}
	}
});
