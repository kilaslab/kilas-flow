import { paraglideVitePlugin } from '@inlang/paraglide-js';
import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

// Production serves the API and the SPA from one origin. The dev proxy
// reproduces that, so application code can always use relative URLs and never
// needs to know which port the backend is on.
//
// Keep these prefixes in step with internal/api/server.go.
//
// /resume is the one that got missed, and its absence is invisible until it
// matters: the approval page POSTs to /resume/<token> to resume a waiting
// execution, and without the rule Vite answers the POST itself with the SPA
// document, so the page reports a successful request that resumed nothing.
const backend = process.env.KILASFLOW_BACKEND_URL ?? 'http://127.0.0.1:8080';

// strictPort keeps the dev URL predictable: silently sliding to the next free
// port makes it ambiguous which server the browser is talking to. Set
// KILASFLOW_WEB_PORT when 5173 is taken by another project.
const port = Number(process.env.KILASFLOW_WEB_PORT ?? 5173);

const proxied = ['/api', '/webhook', '/docs', '/resume', '/oauth'];

export default defineConfig({
	// The strategy is deliberately reduced to the base locale: `$lib/i18n`
	// owns the runtime locale by overriding the generated getLocale, so
	// paraglide's own detection must not be able to read navigator, a cookie,
	// a URL pattern or localStorage. With no urlPatterns the generated runtime
	// needs no URLPattern polyfill either.
	//
	// outdir is untracked on purpose — the generated runtime writes its own
	// `.gitignore` containing `*` — so `i18n:compile` runs before check and
	// test, and a fresh clone can resolve `$lib/paraglide/*`.
	plugins: [
		tailwindcss(),
		sveltekit(),
		paraglideVitePlugin({
			project: './project.inlang',
			outdir: './src/lib/paraglide',
			strategy: ['baseLocale'],
			emitTsDeclarations: true
		})
	],

	server: {
		port,
		strictPort: true,
		proxy: Object.fromEntries(
			proxied.map((path) => [path, { target: backend, changeOrigin: true }])
		)
	}
});
