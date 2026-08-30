// The editor is a client-rendered SPA embedded in the Go binary: there is no
// Node server in production to render on, and the canvas is inherently
// interactive. Disabling SSR and prerendering makes adapter-static emit a
// single index.html fallback (PRD section 13).
export const ssr = false;
export const prerender = false;
