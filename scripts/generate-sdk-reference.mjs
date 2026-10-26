/**
 * Renders the host SDK's API reference into the docs site from its source.
 *
 * The SDK (@kilasflow/sdk, sdk/) is TypeScript whose documentation lives in
 * JSDoc blocks beside the declarations it describes. TypeDoc would render
 * those blocks, but it is not a dependency of any project in this repository
 * and the docs build stays Node-only, so this script does what the other
 * generated references do: read the source of truth and emit committed
 * Markdown, with a --check that fails when the two disagree.
 *
 * Usage:
 *
 *   node scripts/generate-sdk-reference.mjs          # regenerate the page
 *   node scripts/generate-sdk-reference.mjs --check  # fail if the page is stale
 *
 * What it reads — never a copy of any of it:
 *
 * - sdk/package.json, for the entry-point table (the exports map is the
 *   authority on subpaths) and the version;
 * - sdk/src/version.ts, for SDK_VERSION (which the release tooling already
 *   pins against package.json);
 * - sdk/src/server.ts, sdk/src/browser.ts and sdk/src/http.ts, for every
 *   exported symbol and every public KilasFlowClient method, with the first
 *   paragraph of each one's JSDoc.
 *
 * The class's methods are grouped under the `// --- Section ---` markers the
 * source itself uses, so the page's structure is the source's structure. A
 * declaration this script cannot parse is skipped loudly rather than
 * silently: the page ends with the count of symbols rendered, and a dropped
 * symbol changes that count, which the check catches.
 */

import { readFile, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const repoDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const sdkDir = join(repoDir, 'sdk');
const pagePath = join(repoDir, 'docs', 'src', 'content', 'docs', 'reference', 'sdk.md');

/**
 * Parses one TypeScript source file into the declarations a reference page
 * can render: exported symbols and — inside `export class` bodies — public
 * methods, each carrying the JSDoc block that preceded it and the group
 * marker (`// --- Name ---`) it appeared under.
 *
 * Line-based on purpose. The SDK's style is uniform (tabs, one declaration
 * per line, JSDoc above), and a full parser would add a dependency the docs
 * build does not have. When the style changes, this fails loudly instead of
 * rendering something wrong: the page ends with the count of symbols
 * rendered, and a dropped symbol changes that count, which the check catches.
 */
function parseSource(text) {
	const lines = text.split('\n');
	const declarations = [];
	let pendingDoc;
	let group = '';
	let className = '';
	let braceDepth = 0;

	// The first paragraph of a JSDoc block, as one line of prose.
	const firstParagraph = (block) => {
		const text = block.join(' ').replace(/\s+/g, ' ').trim();
		return (text.split(/(?<=\.)\s+(?=[A-Z])/)[0] ?? '').trim();
	};

	for (let index = 0; index < lines.length; index += 1) {
		const line = lines[index];
		const trimmed = line.trim();

		const marker = /^\/\/\s*---\s*(.+?)\s*-+$/.exec(trimmed);
		if (marker) {
			group = marker[1].trim();
			continue;
		}

		if (trimmed.startsWith('/**')) {
			const block = [trimmed.replace(/^\/\*\*/, '').replace(/\*\/$/, '')];
			if (!trimmed.endsWith('*/')) {
				let scan = index + 1;
				for (; scan < lines.length && !lines[scan].trim().endsWith('*/'); scan += 1) {
					block.push(lines[scan].trim().replace(/^\* ?/, ''));
				}
				if (scan < lines.length) block.push(lines[scan].trim().replace(/\*\/$/, ''));
				index = scan;
			}
			const text = firstParagraph(block);
			// A JSDoc block inside a declaration documents a member or a
			// parameter, not the next export; only a block at the top level of
			// the file or the class body attaches to a declaration.
			if (className ? line.startsWith('\t') && !line.startsWith('\t\t') : !line.startsWith('\t')) {
				pendingDoc = text;
			}
			continue;
		}

		braceDepth += (line.match(/\{/g) ?? []).length - (line.match(/\}/g) ?? []).length;
		if (className && braceDepth <= 0) className = '';

		const classMatch = /^export class ([A-Za-z_$][\w$]*)/.exec(trimmed);
		const methodMatch = className && /^\t(?:async )?(?:\* )?([A-Za-z_$][\w$]*)\(/.test(line) && !line.startsWith('\t#')
			? /^\t(?:async )?(?:\* )?([A-Za-z_$][\w$]*)\(/.exec(line)
			: null;
		const exportKind =
			/^export (?:async )?function\*? ([A-Za-z_$][\w$]*)/.exec(trimmed)?.[0] ? ['function', /^export (?:async )?function\*? ([A-Za-z_$][\w$]*)/.exec(trimmed)[1]] :
			/^export interface ([A-Za-z_$][\w$]*)/.exec(trimmed) ? ['interface', /^export interface ([A-Za-z_$][\w$]*)/.exec(trimmed)[1]] :
			/^export type ([A-Za-z_$][\w$]*)/.exec(trimmed) ? ['type', /^export type ([A-Za-z_$][\w$]*)/.exec(trimmed)[1]] :
			/^export const ([A-Za-z_$][\w$]*)/.exec(trimmed) ? ['const', /^export const ([A-Za-z_$][\w$]*)/.exec(trimmed)[1]] :
			null;

		if (classMatch) {
			className = classMatch[1];
			declarations.push({ kind: 'class', name: className, doc: pendingDoc, group, code: `export class ${className}`, line: index + 1 });
			pendingDoc = undefined;
			continue;
		}
		if (methodMatch) {
			declarations.push({ kind: 'method', name: methodMatch[1], doc: pendingDoc, group, code: signatureFrom(lines, index, 'method'), line: index + 1 });
			pendingDoc = undefined;
			continue;
		}
		if (exportKind) {
			declarations.push({ kind: exportKind[0], name: exportKind[1], doc: pendingDoc, group, code: signatureFrom(lines, index, exportKind[0]), line: index + 1 });
			pendingDoc = undefined;
			continue;
		}
		if (trimmed.length > 0 && !trimmed.startsWith('//')) pendingDoc = undefined;
	}
	return declarations;
}

/**
 * Collects a declaration as one collapsed string, stopping where the
 * declaration ends: a method or function at the body's opening brace, a
 * type or const at its semicolon, an interface at its opening brace (the
 * reference names it and quotes its doc; a field list would duplicate the
 * JSDoc that already says what the fields mean).
 *
 * Parentheses and braces are counted separately, because a signature ends
 * where its parentheses have closed and a `{` or `;` follows — a brace
 * inside the parentheses (an object-literal default) must not end it early,
 * and the body's own braces must not extend it.
 */
function signatureFrom(lines, start, mode) {
	let text = '';
	let parens = 0;
	let braces = 0;
	for (let index = start; index < Math.min(lines.length, start + 20); index += 1) {
		const trimmed = lines[index].replace(/^\t/, '').trim();
		text += (text ? ' ' : '') + trimmed;
		for (const character of trimmed) {
			if (character === '(' || character === '[') parens += 1;
			if (character === ')' || character === ']') parens -= 1;
			if (character === '{') braces += 1;
			if (character === '}') braces -= 1;
		}
		const endsDeclaration =
			mode === 'method' || mode === 'function'
				? parens === 0 && (/[{;]$/.test(trimmed) || braces < 0)
				: mode === 'interface'
					? /\{$/.test(trimmed)
					: parens === 0 && braces === 0 && /;$/.test(trimmed);
		if (endsDeclaration) break;
	}
	return text
		.replace(/\s+/g, ' ')
		.replace(/;$/, '')
		.replace(/\s*\{$/, '')
		.trim();
}

function renderDeclaration(entry) {
	const lines = ['```ts', entry.code, '```'];
	if (entry.doc) lines.push('', entry.doc);
	return lines.join('\n');
}

async function main() {
	const check = process.argv.includes('--check');
	const pkg = JSON.parse(await readFile(join(sdkDir, 'package.json'), 'utf8'));
	const versionSource = await readFile(join(sdkDir, 'src', 'version.ts'), 'utf8');
	const sdkVersion = /SDK_VERSION = '([^']+)'/.exec(versionSource)?.[1] ?? pkg.version;
	if (sdkVersion !== pkg.version) {
		throw new Error(`sdk-reference: package.json says ${pkg.version} but src/version.ts says ${sdkVersion} — make sdk-version-check owns this`);
	}

	const server = parseSource(await readFile(join(sdkDir, 'src', 'server.ts'), 'utf8'));
	const browser = parseSource(await readFile(join(sdkDir, 'src', 'browser.ts'), 'utf8'));
	const http = parseSource(await readFile(join(sdkDir, 'src', 'http.ts'), 'utf8'));

	// Methods are grouped under the `// --- Section ---` markers the client
	// class body uses; a method with no marker (a constructor, or a method of
	// a class that has no markers) is grouped under its own class's name.
	// Top-level exports share one "Exports" section, in source order.
	const headingOf = (entry, classNames) => {
		if (entry.kind === 'method') return entry.group || classNames.at(-1) || 'Methods';
		if (entry.kind === 'class') return 'Classes';
		return 'Exports';
	};
	const renderModule = (title, subtitle, declarations) => {
		const lines = [`## ${title}`, '', subtitle, ''];
		const classNames = [];
		for (const entry of declarations) {
			if (entry.kind === 'class') classNames.push(entry.name);
		}
		const groups = [];
		for (const entry of declarations) {
			const heading = headingOf(entry, classNames.slice(0, classNames.indexOf(entry.name) + 1));
			if (!groups.includes(heading)) groups.push(heading);
		}
		for (const heading of groups) {
			lines.push('', `### ${heading}`, '');
			for (const entry of declarations) {
				if (headingOf(entry, classNames.slice(0, classNames.indexOf(entry.name) + 1)) !== heading) continue;
				lines.push(`#### \`${entry.name}\``, '', renderDeclaration(entry), '');
			}
		}
		return lines.join('\n');
	};

	const serverPage = renderModule(
		'Server entry point (`@kilasflow/sdk/server`)',
		'The backend client. It carries the host\'s API credentials, so it must never be bundled into a browser. Construct once per credential: `new KilasFlowClient({ baseUrl, apiKey })`.',
		server
	);
	const browserPage = renderModule(
		'Browser entry point (`@kilasflow/sdk/browser`)',
		'The page half: mount the embedded editor, drive it, and watch a run. Nothing here needs or accepts a privileged API key.',
		browser
	);
	const sharedPage = renderModule(
		'Shared (`@kilasflow/sdk`)',
		'What both halves use. The root entry point re-exports the server and browser modules plus the generated model types, so a non-browser-restricted bundle imports `@kilasflow/sdk` once.',
		http
	);

	const entryRows = Object.entries(pkg.exports ?? {})
		.map(([subpath, conditions]) => `| \`@kilasflow/sdk${subpath === '.' ? '' : subpath.replace(/^\./, '')}\` | \`${conditions.import}\` |`)
		.join('\n');

	const counted = (declarations, kind) => declarations.filter((entry) => entry.kind === kind).length;
	const page = `---
title: Host SDK
description: The @kilasflow/sdk API reference, generated from the SDK's own source — every export of the server and browser entry points, with the JSDoc that documents them.
sidebar:
  order: 6
---

<!-- Generated from sdk/src and sdk/package.json by scripts/generate-sdk-reference.mjs — do not edit by hand. -->

> **Generated reference — \`@kilasflow/sdk\` \`${sdkVersion}\`.** Produced from the SDK's committed source (JSDoc included), not hand-written. To regenerate, run \`make generate-sdk-reference\`. The guide is [Using the host SDK](/guides/sdk/).

The SDK is ESM-only, with no runtime dependencies, and is not yet published to npm — the [install instructions](/guides/sdk/#install-from-a-checkout-until-the-first-release) cover the pre-release path.

## Entry points

The \`exports\` map in \`sdk/package.json\` is the authority; both current subpaths, as this page was generated:

| Import | Module |
| --- | --- |
${entryRows}

${serverPage}

${browserPage}

${sharedPage}
`;

	if (!check) {
		await writeFile(pagePath, page);
		console.log(`sdk-reference: wrote ${pagePath} (SDK ${sdkVersion}, ${counted(server, 'method')} client methods, ${counted(server, 'function') + counted(browser, 'function') + counted(http, 'function')} exported functions)`);
		return;
	}
	let committed;
	try {
		committed = await readFile(pagePath, 'utf8');
	} catch {
		throw new Error('sdk-reference: reference/sdk.md is missing. Run `node scripts/generate-sdk-reference.mjs` and commit the result.');
	}
	if (committed !== page) {
		throw new Error('sdk-reference: reference/sdk.md is stale. Run `node scripts/generate-sdk-reference.mjs` and commit the result.');
	}
	console.log(`sdk-reference: reference/sdk.md fresh (SDK ${sdkVersion}, ${counted(server, 'method')} client methods)`);
}

try {
	await main();
} catch (error) {
	console.error(error.message);
	process.exit(1);
}
