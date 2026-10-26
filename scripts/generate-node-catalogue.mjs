/**
 * Renders the node catalogue and the credential-type reference into the docs
 * site from a real binary.
 *
 * Both pages describe the catalogue a default install serves, which is a
 * product fact: the parameters, ports and credential types a workflow author
 * can pick. Neither page is hand-written, for the same reason the API
 * reference is not — a hand-written copy drifts the first time a node gains a
 * parameter. The data comes from `GET /api/v1/node-types` and
 * `GET /api/v1/credential-types` of a KilasFlow server built and booted right
 * here (the same arrangement as scripts/generate-api-reference.mjs), plus
 * one thing the API does not serve: the n8n mapping table, read from
 * internal/interop/n8n/n8n.go's own source the way scripts/docs-facts greps
 * the handler registrations.
 *
 * Usage:
 *
 *   node scripts/generate-node-catalogue.mjs          # regenerate the pages
 *   node scripts/generate-node-catalogue.mjs --check  # fail if either is stale
 *
 * One structured page per reference rather than one page per node type:
 * 69 types would be 69 pages a reader has to search between, while one page
 * with an index table and deep links is one Ctrl-F. The page's counts are
 * part of the committed output, so the check below is the drift gate: a node
 * added or removed changes the byte-compare, exactly like the catalogue-size
 * facts scripts/docs-facts writes.
 */

import { readFile, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { withServerURL } from './openapi-spec.mjs';

const repoDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const referenceDir = join(repoDir, 'docs', 'src', 'content', 'docs', 'reference');
const cataloguePath = join(referenceDir, 'node-catalogue.md');
const credentialTypesPath = join(referenceDir, 'credential-types.md');

// The n8n importer's mapping table, read from the source that owns it. The
// node-types response cannot carry it: N8NSource is importer plumbing and is
// deliberately absent from the API, so this grep is the same relationship
// scripts/docs-facts has with the handler registrations — read the one place
// that is the truth, never a copy of it.
async function n8nMappings() {
	const source = await readFile(join(repoDir, 'internal', 'interop', 'n8n', 'n8n.go'), 'utf8');
	const mappings = new Map();
	for (const match of source.matchAll(/n8nType:\s*"([^"]+)",\s*kilasType:\s*"([^"]+)"/g)) {
		const [, n8nType, kilasType] = match;
		if (!mappings.has(kilasType)) mappings.set(kilasType, []);
		mappings.get(kilasType).push(n8nType);
	}
	if (mappings.size === 0) {
		throw new Error('node-catalogue: no n8n mappings found in internal/interop/n8n/n8n.go — did the table move?');
	}
	return mappings;
}

function oneLine(text) {
	return String(text ?? '').replace(/\s+/g, ' ').replace(/\|/g, '\\|').trim();
}

// github-slugger's rule, reimplemented for the index's deep links: lowercase,
// drop anything that is not a word character, hyphen or space, spaces to
// hyphens. The link validator checks anchors at build time, so a mismatch
// here fails `make docs-build` rather than shipping a dead link.
function slug(text) {
	return text.toLowerCase().replace(/[^\w\- ]/g, '').replace(/ /g, '-');
}

function renderPorts(ports) {
	if (!ports || ports.length === 0) return '—';
	return ports.map((port) => `\`${port.name}\` (${port.kind}${port.displayName ? `, ${port.displayName}` : ''})`).join(', ');
}

function renderDefault(value) {
	if (value === undefined || value === null || value === '') return '—';
	if (typeof value === 'object') return `\`${JSON.stringify(value)}\``;
	return `\`${String(value)}\``;
}

// renderParameters flattens one node's parameter list into table rows, one
// level of nesting included (dotted keys), with an option parameter's values
// inline — capped, because a picker with twenty options is what the editor
// is for; the page says which values exist, not all of them at any width.
function renderParameters(lines, definition) {
	const parameters = definition.parameters ?? [];
	if (parameters.length === 0) return;
	lines.push('Parameters:');
	lines.push('');
	lines.push('| Parameter | Kind | Required | Default | Description |');
	lines.push('| --- | --- | --- | --- | --- |');
	for (const parameter of parameters) {
		const description = oneLine(parameter.description);
		const options = (parameter.options ?? []).map((option) => option.value ?? option.label);
		const optionNote = options.length > 0 ? ` Options: ${options.slice(0, 12).map((value) => `\`${value}\``).join(', ')}${options.length > 12 ? ', …' : ''}.` : '';
		const modes = (parameter.modes ?? []).map((mode) => mode.name);
		const modeNote = modes.length > 0 ? ` Modes: ${modes.map((mode) => `\`${mode}\``).join(', ')}.` : '';
		lines.push(`| \`${parameter.key}\` | ${parameter.kind} | ${parameter.required ? 'yes' : 'no'} | ${renderDefault(parameter.default)} | ${description}${optionNote}${modeNote} |`);
		for (const field of parameter.fields ?? []) {
			const fieldOptions = (field.options ?? []).map((option) => option.value ?? option.label);
			const fieldOptionNote = fieldOptions.length > 0 ? ` Options: ${fieldOptions.slice(0, 12).map((value) => `\`${value}\``).join(', ')}${fieldOptions.length > 12 ? ', …' : ''}.` : '';
			lines.push(`| \`${parameter.key}.${field.key}\` | ${field.kind} | ${field.required ? 'yes' : 'no'} | ${renderDefault(field.default)} | ${oneLine(field.description)}${fieldOptionNote} |`);
		}
	}
	lines.push('');
}

function renderCatalogue(nodeTypes, credentialTypes, mappings, version) {
	const byType = new Map();
	for (const definition of nodeTypes) {
		if (!byType.has(definition.type)) byType.set(definition.type, []);
		byType.get(definition.type).push(definition);
	}
	const types = [...byType.keys()].sort();
	const pairs = nodeTypes.length;
	const builtinPairs = nodeTypes.filter((definition) => definition.source === 'builtin').length;
	const packPairs = pairs - builtinPairs;
	const builtinTypes = types.filter((type) => byType.get(type)[0].source === 'builtin').length;

	const lines = [];
	lines.push('---');
	lines.push('title: Node catalogue');
	lines.push('description: Every node type a default install serves — parameters, ports, credentials and the n8n equivalent — generated from the live catalogue.');
	lines.push('sidebar:');
	lines.push('  order: 7');
	lines.push('---');
	lines.push('');
	lines.push('<!-- Generated from GET /api/v1/node-types of a real binary by scripts/generate-node-catalogue.mjs — do not edit by hand. -->');
	lines.push('');
	lines.push(`> **Generated reference — KilasFlow \`${version}\`.** Produced from the node catalogue a real binary serves at \`GET /api/v1/node-types\`, not hand-written. The per-type counts and every table below are the committed output, and \`make generate-node-catalogue-check\` fails when the catalogue changes without a regeneration.`);
	lines.push('');
	lines.push(`A default install serves **${types.length} distinct node types** across **${pairs} type-and-version pairs**: ${builtinTypes} types (${builtinPairs} pairs) compiled into the binary and ${types.length - builtinTypes} types (${packPairs} pairs) from the declarative packs it ships with. This page lists every one — the index table first, then a section per type with its parameters, ports, credential requirements and the n8n node it maps to. What a version *resolves to* is in [the node registry](/concepts/node-registry/); what a parameter *does* at run time is in each node\'s own documentation, where it carries one.`);
	lines.push('');
	lines.push("The catalogue a particular install serves can be smaller: a type scoped to other tenants is absent from `GET /node-types` by design (see [Tenant-scoped nodes](/guides/tenant-scoped-nodes/)), and an install with `KILASFLOW_PACKS_DIR` set serves its packs beside these. Ask your own instance for the truth: `curl -s $KILASFLOW_HOST/api/v1/node-types | jq -r '.[].type'`.");
	lines.push('');
	lines.push('## Index');
	lines.push('');
	lines.push('| Type | Name | Versions | Category | Source | n8n equivalent |');
	lines.push('| --- | --- | --- | --- | --- | --- |');
	for (const type of types) {
		const definitions = byType.get(type);
		const newest = definitions[definitions.length - 1];
		const versions = definitions.map((definition) => String(definition.version)).join(', ');
		const n8n = mappings.get(type) ?? [];
		lines.push(`| [\`${type}\`](#${slug(type)}) | ${oneLine(newest.displayName)} | ${versions} | ${oneLine(newest.category)} | ${newest.source} | ${n8n.map((entry) => `\`${entry}\``).join(', ') || '—'} |`);
	}
	lines.push('');
	for (const type of types) {
		const definitions = byType.get(type);
		const newest = definitions[definitions.length - 1];
		lines.push(`## ${type}`);
		lines.push('');
		const description = oneLine(newest.description) || oneLine(newest.displayName);
		lines.push(`${description}${/\.$/.test(description) ? '' : '.'}`);
		lines.push('');
		lines.push('| | |');
		lines.push('| --- | --- |');
		lines.push(`| Display name | ${oneLine(newest.displayName)} |`);
		lines.push(`| Category | ${oneLine(newest.category)}${newest.group?.length ? ` (${newest.group.join(', ')})` : ''} |`);
		lines.push(`| Versions | ${definitions.map((definition) => `\`${definition.version}\``).join(', ')} |`);
		lines.push(`| Inputs | ${renderPorts(newest.inputs)} |`);
		lines.push(`| Outputs | ${renderPorts(newest.outputs)} |`);
		const credentials = newest.credentials ?? [];
		lines.push(`| Credentials | ${credentials.length > 0 ? credentials.map((credential) => `\`${credential.type}\`${credential.required ? ' (required)' : ''}`).join(', ') : '—'} |`);
		const n8n = mappings.get(type) ?? [];
		lines.push(`| n8n equivalent | ${n8n.map((entry) => `\`${entry}\``).join(', ') || '—'} |`);
		if (newest.documentationUrl) lines.push(`| Documentation | ${newest.documentationUrl} |`);
		lines.push('');
		if (definitions.length > 1) {
			lines.push(`Parameters below are version \`${newest.version}\`, the newest; earlier versions may differ.`);
			lines.push('');
		}
		renderParameters(lines, newest);
	}
	return lines.join('\n') + '\n';
}

function renderCredentialTypes(credentialTypes, version) {
	const lines = [];
	lines.push('---');
	lines.push('title: Credential types');
	lines.push('description: Every credential type the server knows — its fields, which are secret, where an empty allowed-domains list may send it — generated from the live catalogue.');
	lines.push('sidebar:');
	lines.push('  order: 8');
	lines.push('---');
	lines.push('');
	lines.push('<!-- Generated from GET /api/v1/credential-types of a real binary by scripts/generate-node-catalogue.mjs — do not edit by hand. -->');
	lines.push('');
	lines.push(`> **Generated reference — KilasFlow \`${version}\`.** Produced from the credential-type catalogue a real binary serves at \`GET /api/v1/credential-types\`, not hand-written.`);
	lines.push('');
	lines.push(`**${credentialTypes.length} credential types ship**, and a node pack adds none — it names one of these. The concepts page covers [how a credential is stored, scoped and tested](/concepts/credentials/); this page is the per-type field reference an integrator or editor renders a form from.`);
	lines.push('');
	lines.push('| ID | Name | Fields | Testable |');
	lines.push('| --- | --- | --- | --- |');
	for (const type of credentialTypes) {
		lines.push(`| [\`${type.id}\`](#${slug(type.id)}) | ${oneLine(type.displayName)} | ${(type.fields ?? []).map((field) => `\`${field.key}\``).join(', ')} | ${type.testable ? 'yes' : 'no'} |`);
	}
	lines.push('');
	for (const type of credentialTypes) {
		lines.push(`## ${type.id}`);
		lines.push('');
		const description = oneLine(type.description) || oneLine(type.displayName);
		lines.push(`${description}${/\.$/.test(description) ? '' : '.'}`);
		lines.push('');
		lines.push('| Field | Required | Secret | Default | Description |');
		lines.push('| --- | --- | --- | --- | --- |');
		for (const field of type.fields ?? []) {
			lines.push(`| \`${field.key}\` | ${field.required ? 'yes' : 'no'} | ${field.secret ? 'yes' : 'no'} | ${renderDefault(field.default)} | ${oneLine(field.description)} |`);
		}
		lines.push('');
		if (type.defaultDomains?.length) {
			lines.push(`An empty allowed-domains list means **${type.defaultDomains.map((domain) => `\`${domain}\``).join(', ')}** for this type.`);
			lines.push('');
		} else if (type.defaultDomainsFrom) {
			lines.push(`An empty allowed-domains list means **the host of \`${type.defaultDomainsFrom}\`** for this type — the address its owner set.`);
			lines.push('');
		} else {
			lines.push('An empty allowed-domains list means **any host** for this type: scope it before giving it to a caller who can choose where a request goes.');
			lines.push('');
		}
		lines.push(type.testable
			? 'The test endpoints can probe this type: `POST /api/v1/credential-types/{type}/test` before it is saved, `POST /api/v1/credentials/{id}/test` after.'
			: 'This type answers `untestable` on the test endpoints — there is no probe that proves it.');
		lines.push('');
	}
	return lines.join('\n') + '\n';
}

async function main() {
	const check = process.argv.includes('--check');
	const mappings = await n8nMappings();
	// One boot serves every read: the two catalogues and the OpenAPI
	// document's info block, which is where the banner's version comes from —
	// the same source the API reference's banner names.
	const { nodeTypes, credentialTypes, version } = await withServerURL(async (baseURL) => {
		const [nodesResponse, credentialsResponse, specResponse] = await Promise.all([
			fetch(`${baseURL}/api/v1/node-types`),
			fetch(`${baseURL}/api/v1/credential-types`),
			fetch(`${baseURL}/api/openapi.json`)
		]);
		if (!nodesResponse.ok || !credentialsResponse.ok) {
			throw new Error(`node-catalogue: catalogue endpoints answered ${nodesResponse.status}/${credentialsResponse.status}`);
		}
		const document = await specResponse.json();
		return {
			nodeTypes: await nodesResponse.json(),
			credentialTypes: await credentialsResponse.json(),
			version: document?.info?.version ?? 'unknown'
		};
	});

	const outputs = new Map();
	outputs.set(cataloguePath, renderCatalogue(nodeTypes, credentialTypes, mappings, version));
	outputs.set(credentialTypesPath, renderCredentialTypes(credentialTypes, version));

	if (!check) {
		for (const [path, content] of outputs) {
			await writeFile(path, content);
		}
		console.log(`node-catalogue: wrote ${outputs.size} pages (${nodeTypes.length} type-and-version pairs, ${nodeTypes.filter((definition) => definition.source === 'pack').length} from packs; ${credentialTypes.length} credential types)`);
		return;
	}
	let failed = false;
	for (const [path, content] of outputs) {
		let committed;
		try {
			committed = await readFile(path, 'utf8');
		} catch {
			console.error(`node-catalogue: ${path} is missing. Run \`node scripts/generate-node-catalogue.mjs\` and commit the result.`);
			failed = true;
			continue;
		}
		if (committed !== content) {
			console.error(`node-catalogue: ${path} is stale. Run \`node scripts/generate-node-catalogue.mjs\` and commit the result.`);
			failed = true;
		}
	}
	if (failed) throw new Error('generated node catalogue is stale');
	console.log(`node-catalogue: ${outputs.size} pages fresh (${nodeTypes.length} pairs, ${credentialTypes.length} credential types)`);
}

try {
	await main();
} catch (error) {
	console.error(error.message);
	process.exit(1);
}
