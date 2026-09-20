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
const PALETTE: Record<number, { fill: string; border: string }> = {
	1: { fill: '#fef3c7', border: '#eab308' },
	2: { fill: '#ffedd5', border: '#f97316' },
	3: { fill: '#fee2e2', border: '#ef4444' },
	4: { fill: '#dcfce7', border: '#22c55e' },
	5: { fill: '#dbeafe', border: '#3b82f6' },
	6: { fill: '#f3e8ff', border: '#a855f7' },
	7: { fill: '#f1f5f9', border: '#64748b' }
};

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
};

/**
 * The markdown subset a sticky note is rendered with.
 *
 * Deliberately a tokenizer that emits runs of text, not HTML: the content
 * comes from an imported third-party file, the editor runs inside customer
 * pages, and the safest renderer is the one that cannot produce markup at all.
 * Links are kept as text for the same reason — a link is a navigation surface,
 * and a note does not need one to be readable.
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
			// Inline code and bold, in one pass so `**a `b` c**` does not nest
			// recursively into itself.
			for (const token of line.split(/(`[^`]*`|\*\*[^*]+\*\*)/g)) {
				if (token === '') continue;
				if (token.startsWith('`') && token.endsWith('`') && token.length > 2) {
					runs.push({ text: token.slice(1, -1), code: true });
					continue;
				}
				if (token.startsWith('**') && token.endsWith('**') && token.length > 4) {
					runs.push({ text: token.slice(2, -2), bold: true });
					continue;
				}
				runs.push({ text: token });
			}
		}
		runs.push({ text: '\n' });
	}
	return runs;
}
