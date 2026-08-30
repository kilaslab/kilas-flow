import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

// Production serves the API and the SPA from one origin. The dev proxy
// reproduces that, so application code can always use relative URLs and never
// needs to know which port the backend is on.
//
// Keep these prefixes in step with internal/api/server.go.
const backend = process.env.KILASFLOW_BACKEND_URL ?? 'http://127.0.0.1:8080';

// strictPort keeps the dev URL predictable: silently sliding to the next free
// port makes it ambiguous which server the browser is talking to. Set
// KILASFLOW_WEB_PORT when 5173 is taken by another project.
const port = Number(process.env.KILASFLOW_WEB_PORT ?? 5173);

const proxied = ['/api', '/webhook', '/docs'];

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],

	server: {
		port,
		strictPort: true,
		proxy: Object.fromEntries(
			proxied.map((path) => [path, { target: backend, changeOrigin: true }])
		)
	}
});
