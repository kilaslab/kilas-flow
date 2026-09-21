import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

/**
 * The copy guard — the mechanical ratchet under "no user-visible English
 * string literal remains in web/src/lib/components and web/src/routes".
 *
 * It reads markup the way a user does: comments, `<script>`, `<style>` and
 * `{…}` expressions are blanked out, and what is left is the text a reader
 * sees plus the attribute values assistive technology reads. It cannot see
 * text that arrives from the Go API or a sentence assembled at runtime, which
 * is why it is a ratchet and not a proof: `PENDING` names every file a later
 * stage still has to migrate, `PENDING` may only shrink, and the files that
 * have already been migrated are checked on every run.
 *
 * A file is added to PENDING only by running the guard and pasting the file
 * paths it reports, sorted — never by hand, and never because a check was
 * inconvenient.
 */
const webRoot = fileURLToPath(new URL('../../..', import.meta.url));
const srcRoot = `${webRoot}/src`;
const guardedRoots = [`${srcRoot}/lib/components`, `${srcRoot}/routes`];
const messagesRoot = `${webRoot}/messages`;
const baseLocale = (
	JSON.parse(readFileSync(`${webRoot}/project.inlang/settings.json`, 'utf8')) as {
		baseLocale: string;
	}
).baseLocale;

/**
 * Text that is not copy: brand tokens and the technical names that read the
 * same in every locale. Every entry needs a reason, because the entry is what
 * stops the walker from ever looking at this string again.
 */
const ALLOWED_TEXT: Record<string, string> = {
	K: 'the KilasFlow brand mark, rendered as a single letterform',
	KilasFlow: 'the product brand name, which is not translated',
	JSON: 'a wire format name, not prose',
	CSV: 'a wire format name, not prose',
	API: 'a technical name, not prose',
	HTTP: 'a protocol name, not prose',
	URL: 'a technical name, not prose',
	ID: 'a technical name, not prose',
	GET: 'an HTTP method name, not prose',
	'/api/v1/health': 'an API path a developer reads, not copy',
	'/api/v1/ready': 'an API path a developer reads, not copy',
	true: 'a JSON boolean literal, the value the parameter stores, not prose',
	false: 'a JSON boolean literal, the value the parameter stores, not prose',
	// The two keys of the import capsule the editor prints above the raw n8n
	// node they came from. They are payload field names rather than prose, and
	// they stay allowed rather than catalogued for a reason an entry alone
	// cannot carry: an `en` value of `type` would put that string in the base
	// catalog, where the echo rule reads it as a quoted literal and reports
	// `Omit<HTMLInputAttributes, "type">` in the already-migrated
	// `src/lib/components/ui/input/input.svelte` — a file that has nothing to do
	// with this panel and no way to stop matching.
	type: 'the imported-capsule payload field names, not prose',
	version: 'the imported-capsule payload field names, not prose'
};

/**
 * Base-locale values the quoted-literal half of the echo rule cannot judge.
 *
 * These ARE copy — unlike `ALLOWED_TEXT` — but each one is also a bare
 * technical token somewhere else in the tree, so a message that renders it as a
 * whole standalone word trips a rule that exists to catch the opposite mistake
 * (copy that was catalogued and then left behind as a literal). The exemption is
 * declared per value rather than per file because the file at fault is a
 * primitive with nothing to do with the message: `{name} input {port}` renders
 * the word `input`, and `ui/input/input.svelte` holds `"data-slot": dataSlot =
 * "input"`; `{label} type {index}` renders `type`, and the same file holds
 * `Omit<HTMLInputAttributes, "type">`. Rewording the message instead would
 * change what a screen reader announces, and the migration's whole promise is
 * that the English did not move.
 *
 * Only the quoted-literal scan skips these. A text node still has to come from
 * the catalog, which is the check that matters for the acceptance criterion.
 */
const ECHO_EXEMPT: Record<string, true> = { input: true, type: true };

/** Every .svelte file the guard reads, as a path relative to web/. */
function guardedFiles(): string[] {
	const found: string[] = [];
	const visit = (directory: string): void => {
		for (const entry of readdirSync(directory, { withFileTypes: true })) {
			const path = `${directory}/${entry.name}`;
			if (entry.isDirectory()) visit(path);
			else if (entry.name.endsWith('.svelte')) found.push(path.slice(webRoot.length + 1));
		}
	};
	for (const root of guardedRoots) visit(root);
	return found.sort();
}

/** Blank every character except newlines, so offsets keep their line numbers. */
function blank(source: string): string {
	return source.replace(/[^\n]/g, ' ');
}

/** Skip a `{…}` expression, honouring nested braces and quoted strings. */
function skipExpression(source: string, start: number): number {
	let depth = 0;
	let quote = '';
	let index = start;
	while (index < source.length) {
		const char = source[index];
		if (quote) {
			if (char === '\\') {
				index += 2;
				continue;
			}
			if (char === quote) quote = '';
			index += 1;
			continue;
		}
		if (char === '"' || char === "'" || char === '`') {
			quote = char;
			index += 1;
			continue;
		}
		if (char === '{') depth += 1;
		if (char === '}') {
			depth -= 1;
			if (depth === 0) return index + 1;
		}
		index += 1;
	}
	return source.length;
}

/** Just the markup a reader sees: comments, style, script and `{…}` blanked. */
function markupOnly(source: string): string {
	let out = '';
	let index = 0;
	while (index < source.length) {
		if (source.startsWith('<!--', index)) {
			const end = source.indexOf('-->', index);
			const stop = end === -1 ? source.length : end + 3;
			out += blank(source.slice(index, stop));
			index = stop;
			continue;
		}
		const element = /^<(script|style)\b/.exec(source.slice(index, index + 8));
		if (element) {
			const close = source.indexOf(`</${element[1]}>`, index);
			const stop = close === -1 ? source.length : close + element[1].length + 3;
			out += blank(source.slice(index, stop));
			index = stop;
			continue;
		}
		if (source[index] === '{') {
			const stop = skipExpression(source, index);
			out += blank(source.slice(index, stop));
			index = stop;
			continue;
		}
		out += source[index];
		index += 1;
	}
	return out;
}

/**
 * The source with its comments blanked, so the echo rule reads code and markup
 * but not prose that never reaches a screen — a doc comment naming a section
 * is documentation, not copy.
 */
function withoutComments(source: string): string {
	let out = '';
	let index = 0;
	let quote = '';
	while (index < source.length) {
		const char = source[index];
		if (quote) {
			out += char;
			if (char === '\\') {
				out += source[index + 1] ?? '';
				index += 2;
				continue;
			}
			if (char === quote) quote = '';
			index += 1;
			continue;
		}
		if (char === '"' || char === "'" || char === '`') {
			quote = char;
			out += char;
			index += 1;
			continue;
		}
		// A line comment ends at the newline, a block comment at its closing
		// delimiter. Searching for the next `//` instead made the blanking
		// alternate: every second comment stayed in the text (a quoted word
		// inside it read as a catalog echo) and the real code between two of
		// them was blanked out, where an echo could not be seen at all.
		const comment = source.startsWith('//', index)
			? '\n'
			: source.startsWith('/*', index)
				? '*/'
				: source.startsWith('<!--', index)
					? '-->'
					: '';
		if (comment) {
			const end = source.indexOf(comment, comment === '\n' ? index : index + comment.length);
			const stop = end === -1 ? source.length : end + comment.length;
			out += blank(source.slice(index, stop));
			index = stop;
			continue;
		}
		out += char;
		index += 1;
	}
	return out;
}

/**
 * Markup split into tags and the text between them. A tag is read to its
 * closing `>` only outside quotes: a class like `[&>svg]:size-4` carries a `>`
 * of its own, and a regex that stopped there would report the rest of the tag
 * as a text node.
 */
function markupParts(markup: string): { tag: boolean; value: string; index: number }[] {
	const parts: { tag: boolean; value: string; index: number }[] = [];
	let index = 0;
	let textStart = 0;
	const flush = (end: number): void => {
		if (end > textStart) parts.push({ tag: false, value: markup.slice(textStart, end), index: textStart });
	};
	while (index < markup.length) {
		if (markup[index] === '<' && (markup[index + 1] === '/' || /[A-Za-z]/.test(markup[index + 1] ?? ''))) {
			flush(index);
			let quote = '';
			let stop = index + 1;
			while (stop < markup.length) {
				const char = markup[stop];
				if (quote) {
					if (char === quote) quote = '';
				} else if (char === '"' || char === "'") quote = char;
				else if (char === '>') break;
				stop += 1;
			}
			parts.push({ tag: true, value: markup.slice(index, stop + 1), index });
			index = stop + 1;
			textStart = index;
			continue;
		}
		index += 1;
	}
	flush(markup.length);
	return parts;
}

function lineOf(source: string, offset: number): number {
	let line = 1;
	for (let index = 0; index < offset && index < source.length; index += 1) {
		if (source[index] === '\n') line += 1;
	}
	return line;
}

/**
 * The string literals handed to the sinks that render straight to a user —
 * an error card, a toast, a dialog. Each is checked for a space and letters
 * because that is what makes it a sentence rather than a code or a token.
 */
const SINK =
	/new\s+Error\s*\(\s*(["'`])([^"'`]*)\1|(?<![\w$])(?:toast|confirm|alert)\s*\(\s*(["'`])([^"'`]*)\3/g;

type Violation = { file: string; line: number; detail: string };

/** Every base-locale catalog value, with its `{param}` placeholders blanked. */
function catalogCopy(): { key: string; text: string }[] {
	const values: { key: string; text: string }[] = [];
	for (const entry of readdirSync(`${messagesRoot}/${baseLocale}`).sort()) {
		if (!entry.endsWith('.json')) continue;
		const catalog = JSON.parse(
			readFileSync(`${messagesRoot}/${baseLocale}/${entry}`, 'utf8')
		) as Record<string, unknown>;
		for (const [key, value] of Object.entries(catalog)) {
			if (typeof value !== 'string') continue;
			const text = value
				.replace(/\{[^}]*\}/g, ' ')
				.replace(/\s+/g, ' ')
				.trim();
			// Values under four characters are too short to tell a copy echo
			// from a coincidence, and the key convention already stops two.
			if (text.length < 4) continue;
			values.push({ key, text });
		}
	}
	return values;
}

const catalog = catalogCopy();

function violationsIn(file: string, source: string): Violation[] {
	const violations: Violation[] = [];
	const markup = markupOnly(source);
	const attribute =
		/(?:^|\s)(title|aria-label|aria-description|placeholder|alt|label)\s*=\s*(?:"([^"]*)"|'([^']*)')/g;

	for (const part of markupParts(markup)) {
		if (part.tag) {
			let found: RegExpExecArray | null;
			attribute.lastIndex = 0;
			while ((found = attribute.exec(part.value))) {
				const value = found[2] ?? found[3] ?? '';
				if (/[A-Za-z]/.test(value) && !Object.hasOwn(ALLOWED_TEXT, value)) {
					violations.push({
						file,
						line: lineOf(source, part.index + found.index),
						detail: `${found[1]}="${value}"`
					});
				}
			}
			continue;
		}
		if (!/[A-Za-z]/.test(part.value)) continue;
		const text = part.value.replace(/\s+/g, ' ').trim();
		if (!text || Object.hasOwn(ALLOWED_TEXT, text)) continue;
		violations.push({ file, line: lineOf(source, part.index), detail: `text "${text}"` });
		// A text node that is still there when the same words are already in
		// the catalog is the shape this migration is meant to remove, so the
		// echo rule reads the same nodes rather than grepping again.
		for (const message of catalog) {
			if (message.text === text) {
				violations.push({ file, line: lineOf(source, part.index), detail: `catalog echo of ${message.key}` });
			}
		}
	}

	const code = withoutComments(source);
	for (const message of catalog) {
		if (Object.hasOwn(ECHO_EXEMPT, message.text)) continue;
		for (const quote of ["'", '"', '`']) {
			const at = code.indexOf(`${quote}${message.text}${quote}`);
			if (at === -1) continue;
			violations.push({
				file,
				line: lineOf(source, at),
				detail: `catalog echo of ${message.key} as a quoted literal`
			});
		}
	}

	let sink: RegExpExecArray | null;
	SINK.lastIndex = 0;
	while ((sink = SINK.exec(source))) {
		const literal = sink[2] ?? sink[4] ?? '';
		if (!/[A-Za-z]/.test(literal) || !/\s/.test(literal)) continue;
		violations.push({ file, line: lineOf(source, sink.index), detail: `user-facing sink "${literal}"` });
	}

	return violations;
}

/**
 * Every file a later stage still had to migrate, generated by running the
 * guard with an empty list and pasting the file paths it reported, sorted.
 * While it was non-empty the guard asserted each entry still existed, so a
 * rename showed up here rather than silently dropping the file out of the
 * ratchet.
 *
 * Stage 4 migrated the last one — the embed shell — so the table is empty and
 * a test below asserts that it stays empty: this is the mechanical proof of
 * "no user-visible English string literal remains in `src/lib/components` and
 * `src/routes`". An entry added here again would have to name a file nobody
 * migrated, which is exactly what that assertion refuses. It stays declared
 * rather than deleted so the next migration has the mechanism, not a memory of
 * it.
 */
const PENDING: Record<string, true> = {};

describe('the copy guard', () => {
	const files = guardedFiles();
	const violations = files.flatMap((file) => violationsIn(file, readFileSync(`${webRoot}/${file}`, 'utf8')));

	it('walks the directories the acceptance criterion names', () => {
		expect(files.length).toBeGreaterThan(100);
	});

	it('has nothing left pending, so every file the walk reaches is migrated', () => {
		expect(Object.keys(PENDING), 'the migration is finished; PENDING must stay empty').toEqual([]);
	});

	it('every pending file still exists, so the ratchet cannot rot', () => {
		const missing = Object.keys(PENDING).filter((path) => !existsSync(`${webRoot}/${path}`)).sort();
		expect(missing, 'a pending entry names a file that is gone').toEqual([]);
	});

	it('no migrated file still carries copy', () => {
		const unguarded = violations
			.filter((violation) => !Object.hasOwn(PENDING, violation.file))
			.map((violation) => `${violation.file}:${violation.line} — ${violation.detail}`);
		expect(unguarded, 'move the copy into web/messages or into an area catalog').toEqual([]);
	});
});
