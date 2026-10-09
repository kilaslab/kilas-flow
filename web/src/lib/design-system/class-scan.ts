// Collects the Tailwind class sets a source file declares, for the design
// lints (focus-lint.ts, the warning rule in tokens.hue.test.ts). Test-only:
// it reads files and parses Svelte with the compiler.
//
// A class set is what one place in the source can put on one element:
//   - in Svelte markup, every class-ish attribute of one element or component
//     (`class`, `contentClass`, …) with its static text, the string literals
//     inside its expressions, and its `class:` directives, plus the class sets
//     of the element's ancestors in the same file;
//   - in a Svelte <script> or a .ts file, one string or template literal (that
//     is where tv() variants and cn() fragments live).

import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';

import { parse } from 'svelte/compiler';

export interface ClassSet {
	file: string;
	line: number;
	/** Element or component name for markup; null for a script literal. */
	element: string | null;
	/** Static attribute values of the element (aria-hidden, tabindex, …). */
	attrs: Record<string, string>;
	/**
	 * Inline style the element can get: the `style` attribute's static text and
	 * the string literals in its expressions, plus every `style:prop`
	 * directive as `prop: <its literals>;`. Over-approximates a dynamic value:
	 * every literal it could take appears.
	 */
	style: string;
	tokens: string[];
	/** Class tokens of each enclosing element in the same file, innermost first. */
	ancestors: string[][];
}

export interface StyleBlock {
	file: string;
	line: number;
	css: string;
}

export interface ScanResult {
	classSets: ClassSet[];
	styles: StyleBlock[];
	/** Every CSS-in-JS `outline: 'none'` style property written in a .ts or <script>. */
	jsOutlineRemovals: Array<{ file: string; line: number; text: string }>;
}

type AnyNode = { type?: string; [key: string]: unknown };

function lineAt(source: string, offset: number): number {
	let line = 1;
	for (let i = 0; i < offset && i < source.length; i += 1) if (source[i] === '\n') line += 1;
	return line;
}

function splitTokens(text: string): string[] {
	return text.split(/\s+/).filter(Boolean);
}

/** String and template-literal text anywhere inside an ESTree subtree. */
function literalTexts(node: unknown, out: Array<{ text: string; start: number }> = []): Array<{ text: string; start: number }> {
	if (!node || typeof node !== 'object') return out;
	if (Array.isArray(node)) {
		for (const child of node) literalTexts(child, out);
		return out;
	}
	const n = node as AnyNode;
	if (n.type === 'Literal' && typeof n.value === 'string') {
		out.push({ text: n.value, start: Number(n.start ?? 0) });
	} else if (n.type === 'TemplateLiteral') {
		const quasis = (n.quasis as AnyNode[]) ?? [];
		out.push({
			text: quasis.map((q) => (q.value as { cooked?: string })?.cooked ?? '').join(' '),
			start: Number(n.start ?? 0)
		});
	}
	for (const [key, value] of Object.entries(n)) {
		if (key === 'quasis' || key === 'loc') continue;
		if (value && typeof value === 'object') literalTexts(value, out);
	}
	return out;
}

const OUTLINE_IN_JS = /\boutline(?:Style)?\s*:\s*['"`](?:none|0)\b[^'"`]*['"`]/g;

function jsOutlineRemovals(file: string, source: string, offset = 0): ScanResult['jsOutlineRemovals'] {
	return [...source.matchAll(OUTLINE_IN_JS)].map((m) => ({
		file,
		line: lineAt(source, offset + (m.index ?? 0)),
		text: m[0]
	}));
}

const CLASS_ATTRIBUTE = /^(class|[a-z][a-zA-Z]*Class)$/;

export function scanSvelte(file: string, source: string): ScanResult {
	const ast = parse(source, { modern: true }) as unknown as AnyNode;
	const result: ScanResult = { classSets: [], styles: [], jsOutlineRemovals: [] };

	for (const script of [ast.instance, ast.module] as AnyNode[]) {
		if (!script) continue;
		for (const lit of literalTexts(script.content)) {
			const tokens = splitTokens(lit.text);
			if (tokens.length === 0) continue;
			result.classSets.push({ file, line: lineAt(source, lit.start), element: null, attrs: {}, style: '', tokens, ancestors: [] });
		}
		const start = Number(script.start ?? 0);
		const end = Number(script.end ?? source.length);
		result.jsOutlineRemovals.push(...jsOutlineRemovals(file, source.slice(start, end), start));
	}

	const css = ast.css as AnyNode | null | undefined;
	if (css) {
		const content = css.content as { styles: string; start: number };
		result.styles.push({ file, line: lineAt(source, content.start), css: content.styles });
	}

	const visit = (node: unknown, ancestors: string[][]): void => {
		if (!node || typeof node !== 'object') return;
		if (Array.isArray(node)) {
			for (const child of node) visit(child, ancestors);
			return;
		}
		const n = node as AnyNode;
		let nextAncestors = ancestors;
		if (Array.isArray(n.attributes) && typeof n.name === 'string') {
			const tokens: string[] = [];
			const attrs: Record<string, string> = {};
			let style = '';
			const partsOf = (value: unknown) =>
				value === true ? [] : Array.isArray(value) ? (value as AnyNode[]) : [value as AnyNode];
			const textOf = (parts: AnyNode[]) =>
				parts
					.map((part) =>
						part.type === 'Text'
							? String(part.data ?? '')
							: literalTexts(part.expression)
									.map((lit) => lit.text)
									.join(' ')
					)
					.join('');
			for (const attr of n.attributes as AnyNode[]) {
				const name = String(attr.name ?? '');
				if (attr.type === 'ClassDirective') {
					tokens.push(name);
					continue;
				}
				if (attr.type === 'StyleDirective') {
					style += `${name}: ${textOf(partsOf(attr.value))};`;
					continue;
				}
				if (attr.type !== 'Attribute') continue;
				const value = attr.value;
				const parts = partsOf(value);
				if (name === 'style') style += `${textOf(parts)};`;
				const isClass = CLASS_ATTRIBUTE.test(name);
				let staticText = '';
				for (const part of parts) {
					if (part.type === 'Text') staticText += String(part.data ?? '');
					else if (isClass && part.type === 'ExpressionTag') {
						for (const lit of literalTexts(part.expression)) tokens.push(...splitTokens(lit.text));
					}
				}
				if (isClass) tokens.push(...splitTokens(staticText));
				else if (parts.every((p) => p.type === 'Text')) attrs[name] = staticText;
			}
			result.classSets.push({
				file,
				line: lineAt(source, Number(n.start ?? 0)),
				element: n.name,
				attrs,
				style,
				tokens,
				ancestors
			});
			nextAncestors = [tokens, ...ancestors];
		}
		for (const [key, value] of Object.entries(n)) {
			if (key === 'attributes' || key === 'expression' || key === 'metadata') continue;
			if (value && typeof value === 'object') visit(value, nextAncestors);
		}
	};
	visit(ast.fragment, []);
	return result;
}

const TS_LITERAL = /'(?:[^'\\\n]|\\.)*'|"(?:[^"\\\n]|\\.)*"|`(?:[^`\\]|\\.)*`/g;

export function scanTs(file: string, source: string): ScanResult {
	const classSets: ClassSet[] = [];
	for (const m of source.matchAll(TS_LITERAL)) {
		const tokens = splitTokens(m[0].slice(1, -1));
		if (tokens.length === 0) continue;
		classSets.push({ file, line: lineAt(source, m.index ?? 0), element: null, attrs: {}, style: '', tokens, ancestors: [] });
	}
	return { classSets, styles: [], jsOutlineRemovals: jsOutlineRemovals(file, source) };
}

/** The web/src tree the lints cover: .svelte and .ts, minus generated code and tests. */
export function sourceFiles(root: string): string[] {
	const out: string[] = [];
	for (const entry of readdirSync(root)) {
		const path = join(root, entry);
		if (entry === 'paraglide' || entry === 'node_modules' || entry === 'generated') continue;
		if (statSync(path).isDirectory()) out.push(...sourceFiles(path));
		else if (/\.(svelte|ts)$/.test(entry) && !/\.(test|spec)\.ts$/.test(entry) && !entry.endsWith('.d.ts')) out.push(path);
	}
	return out.sort();
}

export function scanFile(path: string): ScanResult {
	const source = readFileSync(path, 'utf8');
	return path.endsWith('.svelte') ? scanSvelte(path, source) : scanTs(path, source);
}

/** Splits a token on its top-level variant colons (not those inside [] or ()). */
function variantParts(token: string): string[] {
	const parts: string[] = [];
	let depth = 0;
	let current = '';
	for (const ch of token) {
		if (ch === '[' || ch === '(') depth += 1;
		if (ch === ']' || ch === ')') depth -= 1;
		if (ch === ':' && depth === 0) {
			parts.push(current);
			current = '';
			continue;
		}
		current += ch;
	}
	parts.push(current);
	return parts;
}

/** The utility a token applies, without variants or `!` (`focus-visible:ring-2` → `ring-2`). */
export function utilityOf(token: string): string {
	return (variantParts(token).at(-1) ?? '').replace(/^!|!$/g, '');
}

/** The variants of a token (`dark:focus-visible:ring-2` → ['dark', 'focus-visible']). */
export function variantsOf(token: string): string[] {
	return variantParts(token).slice(0, -1);
}
