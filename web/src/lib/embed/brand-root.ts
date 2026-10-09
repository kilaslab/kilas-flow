// The embed shell carries the host's accent as `--kf-accent-src` on its
// [data-kf-brand] element, but overlays that bits-ui portals to <body> (the
// version-history sheet, the paste report) render outside that element. While
// the embed route is mounted the same value also sits on the document root, so
// every accent step re-derives there too (app.css declares the derived tokens
// on :root and on [data-kf-brand]). The accent arrives already sanitised by
// sanitizeBranding.

export interface BrandTarget {
	setAttribute(name: string, value: string): void;
	removeAttribute(name: string): void;
	style: { setProperty(name: string, value: string): void; removeProperty(name: string): string };
}

/** Brands `root` with `accent` and returns the function that removes it again. */
export function brandDocument(root: BrandTarget, accent: string | undefined): () => void {
	if (!accent) return () => {};
	root.setAttribute('data-kf-brand', '');
	root.style.setProperty('--kf-accent-src', accent);
	return () => {
		root.removeAttribute('data-kf-brand');
		root.style.removeProperty('--kf-accent-src');
	};
}
