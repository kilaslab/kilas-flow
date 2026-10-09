// The focus lint (BUG-ywbvfa criterion 4): no focus indicator is removed
// without a replacement. Test-only; focus-lint.test.ts runs it over web/src
// and web/src/app.css inside `pnpm test`.
//
// KilasFlow draws ONE focus ring: the global `:focus-visible` rule in app.css
// (2px solid --kf-focus, 2px offset). Markup almost never needs a focus class;
// the four project utilities cover the exceptions:
//
//   focus-region       programmatic focus target (tabindex="-1"): the ring is
//                      drawn inset, and only on :focus-visible
//   focus-inset        dense list or grid row: the ring is inset by 1px
//   focus-within-ring  a composite field's container draws the ring while
//                      the control inside it has focus
//   focus-delegate     that inner control, which then draws none itself
//
// Rule A: a class set holding `outline-none`/`outline-hidden` (any variant)
//   must also hold a replacement: a focus-region/focus-inset/focus-within-ring
//   utility, focus-delegate under a focus-within-ring ancestor, or a
//   (focus-visible|focus|has-focus-visible|data-highlighted): outline/ring/
//   border/bg indicator. `focus-delegate` always needs the ancestor. A static
//   `style="outline: none"` is a violation.
// Rule B: CSS `outline: none|0` / `outline-style: none` / `@apply
//   outline-none` only inside a :not(:focus-visible) selector, the
//   focus-region/focus-delegate utilities, or the Svelte Flow node override.
// Rule C: CSS-in-JS `outline: 'none'` only where REVIEWED_JS_REMOVALS says
//   where the replacement ring lives, and that replacement must exist.

import { readFileSync } from 'node:fs';
import { join, relative } from 'node:path';

import { APP_CSS_URL } from './app-css';
import { scanFile, sourceFiles, utilityOf, variantsOf, type ClassSet, type ScanResult } from './class-scan';

export interface Violation {
	file: string;
	line: number;
	rule: 'A' | 'B' | 'C';
	message: string;
}

const REMOVES = new Set(['outline-none', 'outline-hidden']);
const REPLACEMENT_UTILITIES = new Set(['focus-region', 'focus-inset', 'focus-within-ring']);
const INDICATOR_VARIANTS = new Set(['focus-visible', 'focus', 'has-focus-visible', 'data-highlighted']);
const INDICATOR_UTILITY = /^(outline-(?!none$|hidden$)|ring-|border-|bg-)/;

export const HOW_TO_FIX =
	'Delete it: the global :focus-visible rule in app.css draws the one KilasFlow focus ring on every control. ' +
	'If the element is a programmatic focus target (tabindex="-1"), use "focus-region" instead; in a dense list or grid row use "focus-inset"; ' +
	'for the inner input of a composite field use "focus-delegate" and put "focus-within-ring" on the field container. ' +
	'See the focus utilities in web/src/app.css.';

const hasIndicator = (tokens: string[]) =>
	tokens.some((t) => {
		const variants = variantsOf(t);
		return variants.some((v) => INDICATOR_VARIANTS.has(v)) && INDICATOR_UTILITY.test(utilityOf(t));
	});

const bare = (tokens: string[], utility: string) => tokens.some((t) => utilityOf(t) === utility && variantsOf(t).length === 0);

const OUTLINE_NONE_STYLE = /(^|;)\s*outline(-style)?\s*:\s*(none|0)\b/;

function where(set: ClassSet): string {
	return set.element ? `<${set.element}>` : 'string literal';
}

export function lintClassSets(sets: ClassSet[]): Violation[] {
	const out: Violation[] = [];
	for (const set of sets) {
		const delegating = bare(set.tokens, 'focus-delegate');
		const ancestorRing = set.ancestors.some((tokens) => bare(tokens, 'focus-within-ring'));
		if (delegating && !ancestorRing) {
			out.push({
				file: set.file,
				line: set.line,
				rule: 'A',
				message:
					`${where(set)} uses "focus-delegate", which hides its own ring, but no enclosing element in this file carries "focus-within-ring" to draw it. ` +
					'Add "focus-within-ring" to the field container, or drop "focus-delegate" and let the global ring draw on the control.'
			});
		}
		if (set.attrs.style && OUTLINE_NONE_STYLE.test(set.attrs.style)) {
			out.push({ file: set.file, line: set.line, rule: 'A', message: `${where(set)} sets "outline: none" inline. ${HOW_TO_FIX}` });
		}
		const removing = set.tokens.filter((t) => REMOVES.has(utilityOf(t)));
		if (removing.length === 0) continue;
		const replaced =
			[...REPLACEMENT_UTILITIES].some((u) => bare(set.tokens, u)) || hasIndicator(set.tokens) || (delegating && ancestorRing);
		if (replaced) continue;
		out.push({
			file: set.file,
			line: set.line,
			rule: 'A',
			message: `${where(set)} "${removing.join(' ')}" removes the focus ring with no replacement. ${HOW_TO_FIX}`
		});
	}
	return out;
}

/** Rule B over one stylesheet (app.css or a Svelte <style> block). */
export function lintCss(file: string, css: string, firstLine = 1): Violation[] {
	const out: Violation[] = [];
	const text = css.replace(/\/\*[\s\S]*?\*\//g, (comment) => comment.replace(/[^\n]/g, ' '));
	const stack: string[] = [];
	let statement = '';
	let line = firstLine;
	let statementLine = firstLine;

	const allowed = () =>
		stack.some(
			(prelude) =>
				prelude.includes(':not(:focus-visible)') ||
				/^@utility\s+(focus-region|focus-delegate)\b/.test(prelude) ||
				prelude.replace(/\s+/g, '') === '.svelte-flow__node.selectable:focus-visible'
		);

	const check = (decl: string) => {
		const d = decl.trim();
		const removesOutline = /^outline\s*:\s*(none|0)\b/.test(d) || /^outline-style\s*:\s*none\b/.test(d);
		const applies = d.startsWith('@apply') && d.split(/\s+/).slice(1).some((t) => REMOVES.has(utilityOf(t)));
		if ((removesOutline || applies) && !allowed()) {
			out.push({
				file,
				line: statementLine,
				rule: 'B',
				message: `"${d}" in "${stack.at(-1) ?? '(top level)'}" removes the focus ring. ${HOW_TO_FIX}`
			});
		}
	};

	for (const ch of text) {
		if (ch === '\n') line += 1;
		if (ch === '{') {
			stack.push(statement.trim());
			statement = '';
			continue;
		}
		if (ch === '}') {
			if (statement.trim()) check(statement);
			stack.pop();
			statement = '';
			continue;
		}
		if (ch === ';') {
			check(statement);
			statement = '';
			continue;
		}
		if (statement.trim() === '' && !/\s/.test(ch)) statementLine = line;
		statement += ch;
	}
	return out;
}

/**
 * CSS-in-JS outline removals that were reviewed: where each one's
 * replacement ring is drawn. Rule C fails on any other one, and on a listed
 * one whose replacement has gone.
 */
export const REVIEWED_JS_REMOVALS: Array<{ file: string; replacement: { file: string; utility: string }; reason: string }> = [
	{
		file: 'lib/workflow-editor/code-editor.ts',
		replacement: { file: 'lib/components/workflow-editor/code-editor.svelte', utility: 'focus-within-ring' },
		reason: "CodeMirror's dotted .cm-focused outline is replaced by the host container's focus-within-ring"
	}
];

export function lintJsOutline(removals: ScanResult['jsOutlineRemovals'], srcRoot: string): Violation[] {
	const out: Violation[] = [];
	for (const removal of removals) {
		const rel = relative(srcRoot, removal.file);
		const reviewed = REVIEWED_JS_REMOVALS.find((entry) => entry.file === rel);
		if (!reviewed) {
			out.push({
				file: removal.file,
				line: removal.line,
				rule: 'C',
				message: `${removal.text} removes the focus ring from script-built styles. Draw the replacement (for example "focus-within-ring" on the host element) and add the file to REVIEWED_JS_REMOVALS in focus-lint.ts with where the ring lives.`
			});
			continue;
		}
		const host = scanFile(join(srcRoot, reviewed.replacement.file));
		if (!host.classSets.some((set) => bare(set.tokens, reviewed.replacement.utility))) {
			out.push({
				file: removal.file,
				line: removal.line,
				rule: 'C',
				message: `${removal.text} is reviewed only while ${reviewed.replacement.file} carries "${reviewed.replacement.utility}" (${reviewed.reason}); it no longer does.`
			});
		}
	}
	return out;
}

export function lintTree(srcRoot: string): Violation[] {
	const out: Violation[] = [];
	for (const file of sourceFiles(srcRoot)) {
		// The lint's own sources name the classes it looks for.
		if (/\/design-system\/(focus-lint|class-scan)\.ts$/.test(file)) continue;
		const scan = scanFile(file);
		out.push(...lintClassSets(scan.classSets));
		for (const style of scan.styles) out.push(...lintCss(style.file, style.css, style.line));
		out.push(...lintJsOutline(scan.jsOutlineRemovals, srcRoot));
	}
	out.push(...lintCss(APP_CSS_URL.pathname, readFileSync(APP_CSS_URL, 'utf8')));
	return out;
}

export function formatViolations(violations: Violation[]): string {
	return violations
		.map((v) => `${v.file.replace(/^.*\/web\//, 'web/')}:${v.line} [${v.rule}] ${v.message}`)
		.join('\n');
}
