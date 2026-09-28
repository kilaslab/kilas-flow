/**
 * Sticky notes: the palette, and the markdown subset the canvas renders.
 *
 * An imported template carries its setup instructions in sticky notes — 91 of
 * the top 100 templates have them — so they are the one annotation a user has
 * to be able to read on the canvas.
 */

/**
 * n8n's sticky palette, by the index the importer carries through.
 *
 * The index is data, so the colours are looked up rather than parsed: a note
 * that arrived with colour 3 keeps the colour its author picked.
 */
/**
 * The palette is a set of CSS custom properties rather than hex values,
 * because the fill has to flip with the theme: pastel fills under the dark
 * theme's near-white node labels read at roughly 1.2:1 — the Webhook,
 * "Create URL string" and "Respond to Webhook" names of an imported template
 * were effectively invisible. The dark theme tints dark and keeps the light
 * label; the light theme keeps n8n's pastels and needs the dark label, which
 * the sticky content colour (var(--kf-sticky-text)) flips with it. app.css
 * owns the values; the indices stay the importer's contract.
 */
const PALETTE: Record<number, { fill: string; border: string }> = {
	1: { fill: 'var(--kf-sticky-1-fill)', border: 'var(--kf-sticky-1-border)' },
	2: { fill: 'var(--kf-sticky-2-fill)', border: 'var(--kf-sticky-2-border)' },
	3: { fill: 'var(--kf-sticky-3-fill)', border: 'var(--kf-sticky-3-border)' },
	4: { fill: 'var(--kf-sticky-4-fill)', border: 'var(--kf-sticky-4-border)' },
	5: { fill: 'var(--kf-sticky-5-fill)', border: 'var(--kf-sticky-5-border)' },
	6: { fill: 'var(--kf-sticky-6-fill)', border: 'var(--kf-sticky-6-border)' },
	7: { fill: 'var(--kf-sticky-7-fill)', border: 'var(--kf-sticky-7-border)' }
};

/**
 * The swatch colours the inspector's palette control shows, in palette order.
 * They are the light-theme pastels — read against the control's own surface
 * rather than against a sticky — because a swatch identifies the choice by
 * looking like what a light-theme note will become, which is the n8n mental
 * model the palette carries.
 */
export const STICKY_SWATCHES: { fill: string; border: string }[] = [
	{ fill: '#fef3c7', border: '#eab308' },
	{ fill: '#ffedd5', border: '#f97316' },
	{ fill: '#fee2e2', border: '#ef4444' },
	{ fill: '#dcfce7', border: '#22c55e' },
	{ fill: '#dbeafe', border: '#3b82f6' },
	{ fill: '#f3e8ff', border: '#a855f7' },
	{ fill: '#f1f5f9', border: '#64748b' }
];

export function stickyPalette(color: unknown): { fill: string; border: string } {
	const index = typeof color === 'number' ? Math.round(color) : Number(color);
	return PALETTE[index] ?? PALETTE[1];
}

export type MarkdownRun = {
	text: string;
	/** Emphasised, and rendered as one span rather than as markup. */
	bold?: boolean;
	code?: boolean;
	/** A heading line: only its level is reported, the canvas decides the size. */
	heading?: boolean;
	/** A markdown link: `text` is the label, this is its http(s) target. */
	link?: string;
	/** A markdown image: `text` is the alt text, shown as a chip. */
	image?: boolean;
	/** A list item line: the canvas prints its own marker. */
	bullet?: boolean;
};

/**
 * The markdown subset a sticky note is rendered with.
 *
 * Deliberately a tokenizer that emits runs of text, not HTML: the content
 * comes from an imported third-party file, the editor runs inside customer
 * pages, and the safest renderer is the one that cannot produce markup at all.
 *
 * Links and images are recognised, not copied through: a template's setup note
 * used to print `[![Execute Workflow](https://…gif)](https://…)` literally.
 * A link run carries only an http(s) target — the canvas renders it as a real
 * anchor with rel=noopener, and anything else (a javascript: URL) stays text.
 * An image becomes its alt text in a chip, because a note has no fetch to
 * spend on a GIF.
 */
export function markdownRuns(content: unknown): MarkdownRun[] {
	const text = typeof content === 'string' ? content : '';
	if (text === '') return [];
	const runs: MarkdownRun[] = [];
	for (const line of text.split(/\r?\n/)) {
		const heading = /^(#{1,6})\s+(.*)$/.exec(line);
		if (heading) {
			runs.push({ text: heading[2], heading: true, bold: true });
		} else {
			const bullet = /^(?:[-*+]|\d+[.)])\s+/.exec(line);
			const body = bullet ? line.slice(bullet[0].length) : line;
			const lineStart = runs.length;
			// Images, links, inline code and bold, in one pass so `**a `b` c**`
			// does not nest recursively into itself.
			for (const token of body.split(/(\[!\[[^\]]*\]\((?:[^()]*|\([^()]*\))*\)\]\((?:[^()]*|\([^()]*\))*\)|!\[[^\]]*\]\((?:[^()]*|\([^()]*\))*\)|\[[^\]]*\]\((?:[^()]*|\([^()]*\))*\)|`[^`]*`|\*\*[^*]+\*\*)/g)) {
				if (token === '') continue;
				if (token.startsWith('`') && token.endsWith('`') && token.length > 2) {
					runs.push({ text: token.slice(1, -1), code: true });
					continue;
				}
				if (token.startsWith('**') && token.endsWith('**') && token.length > 4) {
					runs.push({ text: token.slice(2, -2), bold: true });
					continue;
				}
				// A linked image — how template notes embed a "watch the video"
				// button — becomes a link labelled by the alt text: the chip keeps
				// nothing to click, and the URL is the payload worth keeping.
				const wrappedImage = /^\[!\[([^\]]*)\]\((?:[^()]*|\([^()]*\))*\)\]\(([^()]*(?:\([^()]*\)[^()]*)*)\)$/.exec(token);
				if (wrappedImage && /^https?:\/\//i.test(wrappedImage[2].trim())) {
					const target = wrappedImage[2].trim();
					runs.push({ text: wrappedImage[1].trim() || target, link: target });
					continue;
				}
				const image = /^!\[([^\]]*)\]\(([^()]*(?:\([^()]*\)[^()]*)*)\)$/.exec(token);
				if (image) {
					runs.push({ text: image[1].trim() || 'image', image: true });
					continue;
				}
				const link = /^\[([^\]]*)\]\(([^()]*(?:\([^()]*\)[^()]*)*)\)$/.exec(token);
				if (link && /^https?:\/\//i.test(link[2].trim())) {
					runs.push({ text: link[1].trim() || link[2].trim(), link: link[2].trim() });
					continue;
				}
				runs.push({ text: token });
			}
			// The marker belongs to the line's own first run, never to a previous
			// line's — an empty "- " line has none to tag.
			if (bullet && runs.length > lineStart) runs[lineStart] = { ...runs[lineStart], bullet: true };
		}
		runs.push({ text: '\n' });
	}
	return runs;
}
