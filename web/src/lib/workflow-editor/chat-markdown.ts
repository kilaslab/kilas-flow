/**
 * The markdown subset a chat reply is rendered with.
 *
 * Models answer in markdown whether or not anyone asked them to, and a reply
 * shown as raw text reads as `**7,006,652**` with every list collapsed into one
 * line. The parser returns a small tree the panel renders as Svelte elements —
 * never as an HTML string — so a reply can never inject markup, whatever the
 * model or a tool put in it.
 *
 * It is deliberately forgiving: a streamed reply is parsed on every chunk, so an
 * unterminated fence or emphasis must still produce something sensible rather
 * than flicker between shapes.
 */

export type ChatInline =
	| { kind: 'text'; text: string }
	| { kind: 'strong'; children: ChatInline[] }
	| { kind: 'em'; children: ChatInline[] }
	| { kind: 'code'; text: string }
	| { kind: 'link'; href: string; children: ChatInline[] }
	| { kind: 'break' };

export type ChatBlock =
	| { kind: 'paragraph'; inline: ChatInline[] }
	| { kind: 'heading'; level: 1 | 2 | 3; inline: ChatInline[] }
	| { kind: 'list'; ordered: boolean; start: number; items: ChatInline[][] }
	| { kind: 'code'; lang: string; text: string }
	| { kind: 'quote'; inline: ChatInline[] };

const FENCE = /^\s*```\s*([\w+-]*)\s*$/;
const HEADING = /^(#{1,6})\s+(.*)$/;
const QUOTE = /^>\s?(.*)$/;
const BULLET = /^\s*[-*+]\s+(.*)$/;
const ORDERED = /^\s*(\d{1,9})[.)]\s+(.*)$/;
const CONTINUATION = /^\s{2,}\S/;
const SAFE_HREF = /^(https?:\/\/|mailto:)/i;

export function parseChatMarkdown(source: string): ChatBlock[] {
	const lines = source.replace(/\r\n?/g, '\n').split('\n');
	const blocks: ChatBlock[] = [];
	let paragraph: string[] = [];
	let quote: string[] = [];
	let list: { ordered: boolean; start: number; items: string[][] } | null = null;

	const flushParagraph = () => {
		if (paragraph.length > 0) blocks.push({ kind: 'paragraph', inline: inlineLines(paragraph) });
		paragraph = [];
	};
	const flushQuote = () => {
		if (quote.length > 0) blocks.push({ kind: 'quote', inline: inlineLines(quote) });
		quote = [];
	};
	const flushList = () => {
		if (list) {
			blocks.push({ kind: 'list', ordered: list.ordered, start: list.start, items: list.items.map(inlineLines) });
		}
		list = null;
	};
	const flushAll = () => {
		flushParagraph();
		flushQuote();
		flushList();
	};

	for (let index = 0; index < lines.length; index += 1) {
		const line = lines[index];

		const fence = FENCE.exec(line);
		if (fence) {
			flushAll();
			const body: string[] = [];
			index += 1;
			while (index < lines.length && !FENCE.test(lines[index])) {
				body.push(lines[index]);
				index += 1;
			}
			blocks.push({ kind: 'code', lang: fence[1] ?? '', text: body.join('\n') });
			continue;
		}

		if (line.trim() === '') {
			flushAll();
			continue;
		}

		const heading = HEADING.exec(line);
		if (heading) {
			flushAll();
			const level = Math.min(heading[1].length, 3) as 1 | 2 | 3;
			blocks.push({ kind: 'heading', level, inline: parseInline(heading[2].trim()) });
			continue;
		}

		const quoted = QUOTE.exec(line);
		if (quoted) {
			flushParagraph();
			flushList();
			quote.push(quoted[1]);
			continue;
		}

		const ordered = ORDERED.exec(line);
		const bullet = ordered ? null : BULLET.exec(line);
		if (ordered || bullet) {
			flushParagraph();
			flushQuote();
			const isOrdered = Boolean(ordered);
			const text = ordered ? ordered[2] : bullet![1];
			if (!list || list.ordered !== isOrdered) {
				flushList();
				list = { ordered: isOrdered, start: ordered ? Number(ordered[1]) : 1, items: [] };
			}
			list.items.push([text]);
			continue;
		}

		if (list && CONTINUATION.test(line)) {
			list.items[list.items.length - 1].push(line.trim());
			continue;
		}

		flushList();
		flushQuote();
		paragraph.push(line);
	}
	flushAll();
	return blocks;
}

/** Inline content for consecutive lines, joined by explicit breaks. */
function inlineLines(lines: string[]): ChatInline[] {
	const out: ChatInline[] = [];
	lines.forEach((line, index) => {
		if (index > 0) out.push({ kind: 'break' });
		out.push(...parseInline(line.trim()));
	});
	return out;
}

/**
 * Inline spans, left to right. An opening marker without a closing one is kept
 * as the literal text it is — `2 * 3`, or a half-streamed `**bold`.
 */
export function parseInline(text: string): ChatInline[] {
	const out: ChatInline[] = [];
	let buffer = '';
	const pushText = () => {
		if (buffer) out.push({ kind: 'text', text: buffer });
		buffer = '';
	};

	let index = 0;
	while (index < text.length) {
		const rest = text.slice(index);

		if (rest[0] === '`') {
			const end = text.indexOf('`', index + 1);
			if (end > index + 1) {
				pushText();
				out.push({ kind: 'code', text: text.slice(index + 1, end) });
				index = end + 1;
				continue;
			}
		}

		const strongMarker = rest.startsWith('**') ? '**' : rest.startsWith('__') ? '__' : null;
		if (strongMarker) {
			const end = text.indexOf(strongMarker, index + 2);
			if (end > index + 2) {
				pushText();
				out.push({ kind: 'strong', children: parseInline(text.slice(index + 2, end)) });
				index = end + 2;
				continue;
			}
		}

		if ((rest[0] === '*' || rest[0] === '_') && rest[1] && rest[1] !== ' ' && rest[1] !== rest[0]) {
			const marker = rest[0];
			const end = text.indexOf(marker, index + 1);
			const previous = index > 0 ? text[index - 1] : ' ';
			// `_` inside a word (snake_case) is not emphasis.
			const wordBoundary = marker === '*' || /[\s(]/.test(previous);
			if (end > index + 1 && text[end - 1] !== ' ' && wordBoundary) {
				pushText();
				out.push({ kind: 'em', children: parseInline(text.slice(index + 1, end)) });
				index = end + 1;
				continue;
			}
		}

		if (rest[0] === '[') {
			const link = /^\[([^\]]+)\]\(([^)\s]+)\)/.exec(rest);
			if (link && SAFE_HREF.test(link[2])) {
				pushText();
				out.push({ kind: 'link', href: link[2], children: parseInline(link[1]) });
				index += link[0].length;
				continue;
			}
			if (link) {
				buffer += link[0];
				index += link[0].length;
				continue;
			}
		}

		buffer += rest[0];
		index += 1;
	}
	pushText();
	return out;
}
