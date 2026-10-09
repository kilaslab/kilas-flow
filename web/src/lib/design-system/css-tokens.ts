// Reads the design tokens out of web/src/app.css for the token tests. It is a
// deliberately small reader for the shape that file has, not a CSS parser:
//
//   - plain `:root { … }`                         → theme-independent scales
//   - a selector list holding [data-theme='dark'] → the dark colour block
//   - a selector list holding [data-theme='light']→ the light colour block
//   - a selector list holding [data-kf-brand]     → derived and alias tokens,
//     re-declared at every theme and brand boundary
//   - the `@supports not (color: oklch(from …))` block → static fallbacks
//
// Every other rule and at-rule (@import, @custom-variant, @theme, @layer,
// @media, .svelte-flow …) is skipped. `tokenEnv` merges the layers the way the
// cascade does on an element that carries a theme (and optionally an inline
// --kf-accent-src), then resolves var() recursively.

import { parseCssColor, type Oklch } from './color';

export type Theme = 'dark' | 'light';
export type Declarations = Map<string, string>;

export interface TokenSheet {
	scales: Declarations;
	dark: Declarations;
	light: Declarations;
	derived: Declarations;
	fallback: { dark: Declarations; light: Declarations };
}

function stripComments(css: string): string {
	return css.replace(/\/\*[\s\S]*?\*\//g, '');
}

/** Index of the brace that closes the one opened at `open`. */
function matchingBrace(css: string, open: number): number {
	let depth = 0;
	for (let i = open; i < css.length; i += 1) {
		if (css[i] === '{') depth += 1;
		if (css[i] === '}') {
			depth -= 1;
			if (depth === 0) return i;
		}
	}
	throw new Error(`css-tokens: unbalanced brace at offset ${open}`);
}

interface Rule {
	selectors: string[];
	body: string;
}

/** Splits a stylesheet body into top-level rules and at-rules. */
function walk(css: string): { rules: Rule[]; atRules: Array<{ prelude: string; body: string }> } {
	const rules: Rule[] = [];
	const atRules: Array<{ prelude: string; body: string }> = [];
	let i = 0;
	while (i < css.length) {
		while (i < css.length && /\s/.test(css[i])) i += 1;
		if (i >= css.length) break;
		const brace = css.indexOf('{', i);
		const semi = css.indexOf(';', i);
		if (css[i] === '@' && semi !== -1 && (brace === -1 || semi < brace)) {
			i = semi + 1; // statement at-rule: @import, @custom-variant
			continue;
		}
		if (brace === -1) break;
		const prelude = css.slice(i, brace).trim();
		const close = matchingBrace(css, brace);
		const body = css.slice(brace + 1, close);
		if (prelude.startsWith('@')) atRules.push({ prelude, body });
		else rules.push({ selectors: splitSelectors(prelude), body });
		i = close + 1;
	}
	return { rules, atRules };
}

function splitSelectors(prelude: string): string[] {
	return prelude.split(',').map((s) => s.trim().replace(/"/g, "'"));
}

/** Top-level `name: value` declarations of a rule body; nested rules are skipped. */
function declarations(body: string): Declarations {
	const out: Declarations = new Map();
	let depth = 0;
	let current = '';
	const flush = () => {
		const colon = current.indexOf(':');
		if (colon > 0) {
			const name = current.slice(0, colon).trim();
			const value = current.slice(colon + 1).trim().replace(/\s+/g, ' ');
			if (name && !name.includes('{') && !/\s/.test(name)) out.set(name, value);
		}
		current = '';
	};
	for (const ch of body) {
		if (ch === '(' || ch === '{') depth += 1;
		if (ch === ')' || ch === '}') depth -= 1;
		if (ch === ';' && depth === 0) {
			flush();
			continue;
		}
		if (ch === '}' && depth === 0) {
			current = ''; // end of a nested rule; drop it
			continue;
		}
		current += ch;
	}
	flush();
	return out;
}

const DARK = "[data-theme='dark']";
const LIGHT = "[data-theme='light']";
const BRAND = '[data-kf-brand]';
const RELATIVE_FALLBACK = /^@supports\s+not\s*\(\s*color\s*:\s*oklch\(\s*from\b/;

function mergeInto(target: Declarations, source: Declarations): void {
	for (const [k, v] of source) target.set(k, v);
}

export function parseTokenSheet(css: string): TokenSheet {
	const sheet: TokenSheet = {
		scales: new Map(),
		dark: new Map(),
		light: new Map(),
		derived: new Map(),
		fallback: { dark: new Map(), light: new Map() }
	};
	const { rules, atRules } = walk(stripComments(css));
	for (const rule of rules) {
		const decls = declarations(rule.body);
		if (rule.selectors.includes(BRAND)) mergeInto(sheet.derived, decls);
		else if (rule.selectors.includes(LIGHT)) mergeInto(sheet.light, decls);
		else if (rule.selectors.includes(DARK)) mergeInto(sheet.dark, decls);
		else if (rule.selectors.length === 1 && rule.selectors[0] === ':root') mergeInto(sheet.scales, decls);
	}
	for (const at of atRules) {
		if (!RELATIVE_FALLBACK.test(at.prelude)) continue;
		for (const rule of walk(at.body).rules) {
			const target = rule.selectors.includes(LIGHT) ? sheet.fallback.light : sheet.fallback.dark;
			mergeInto(target, declarations(rule.body));
		}
	}
	return sheet;
}

export interface TokenEnv {
	/** Every custom property visible on an element of this theme. */
	names(): string[];
	/** The declared text, before var() substitution. */
	raw(name: string): string;
	/** The value with every var() substituted. */
	resolve(name: string): string;
	color(name: string): Oklch;
}

/**
 * The custom properties an element carrying `theme` sees: scales, the dark
 * block (bare :root), the light block when light, the derived block, then
 * `overrides` as an inline style would set them.
 */
export function tokenEnv(sheet: TokenSheet, theme: Theme, overrides: Record<string, string> = {}): TokenEnv {
	const merged: Declarations = new Map();
	mergeInto(merged, sheet.scales);
	mergeInto(merged, sheet.dark);
	if (theme === 'light') mergeInto(merged, sheet.light);
	mergeInto(merged, sheet.derived);
	for (const [k, v] of Object.entries(overrides)) merged.set(k, v);

	const cache = new Map<string, string>();

	const substitute = (text: string, stack: string[]): string => {
		let out = '';
		let i = 0;
		while (i < text.length) {
			const at = text.indexOf('var(', i);
			if (at === -1) {
				out += text.slice(i);
				break;
			}
			out += text.slice(i, at);
			let depth = 0;
			let end = at + 3;
			for (; end < text.length; end += 1) {
				if (text[end] === '(') depth += 1;
				if (text[end] === ')') {
					depth -= 1;
					if (depth === 0) break;
				}
			}
			const inner = text.slice(at + 4, end);
			const comma = inner.indexOf(',');
			const name = (comma === -1 ? inner : inner.slice(0, comma)).trim();
			const fallback = comma === -1 ? undefined : inner.slice(comma + 1).trim();
			if (merged.has(name)) out += resolveName(name, stack);
			else if (fallback !== undefined) out += substitute(fallback, stack);
			else throw new Error(`css-tokens: ${stack.at(-1) ?? '?'} references undefined ${name}`);
			i = end + 1;
		}
		return out;
	};

	const resolveName = (name: string, stack: string[]): string => {
		const hit = cache.get(name);
		if (hit !== undefined) return hit;
		if (stack.includes(name)) throw new Error(`css-tokens: var() cycle ${[...stack, name].join(' → ')}`);
		const value = merged.get(name);
		if (value === undefined) throw new Error(`css-tokens: ${name} is not defined for this theme`);
		const out = substitute(value, [...stack, name]);
		cache.set(name, out);
		return out;
	};

	return {
		names: () => [...merged.keys()],
		raw: (name) => {
			const value = merged.get(name);
			if (value === undefined) throw new Error(`css-tokens: ${name} is not defined for this theme`);
			return value;
		},
		resolve: (name) => resolveName(name, []),
		color: (name) => parseCssColor(resolveName(name, []))
	};
}
