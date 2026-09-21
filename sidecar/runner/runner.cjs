'use strict';

// The KilasFlow JavaScript sidecar runner.
//
// One file, Node built-ins only, no dependency of its own, and it is written
// clean-room: it implements the protocol in sidecar/protocol.go and the usage
// shape a community node package expects — one class per file with a
// `description`, an `execute()` or a trigger method, `getInputData`,
// `getNodeParameter`, `getCredentials` and `helpers.httpRequest`. No code,
// types or bytes from any other node runtime appear here; the packages this
// loads are installed by the operator and are never shipped with KilasFlow.
//
// Usage:
//   node --permission --allow-fs-read=<runner> --allow-fs-read=<packages dir> \
//     runner.cjs --role=run --tenant=<tenant> --socket=<path> \
//     --packages-dir=<absolute path> --package=<name> [--package=<name> ...]
//
// The role is `describe` (load the catalogue, empty tenant) or `run`. stdout
// and stderr are diagnostics only: every protocol byte travels on the socket,
// so a banner at require time cannot desynchronise a run.

const net = require('node:net');
const fs = require('node:fs');
const path = require('node:path');

// --- flags -----------------------------------------------------------------

// parseFlags splits each argument on its first `=` only, so a package name or
// a socket path may contain `=`. Repeated --package flags accumulate.
function parseFlags(argv) {
	const flags = { packages: [] };
	for (const argument of argv) {
		if (!argument.startsWith('--')) continue;
		const body = argument.slice(2);
		const separator = body.indexOf('=');
		if (separator < 0) continue;
		const key = body.slice(0, separator);
		const value = body.slice(separator + 1);
		if (key === 'package') flags.packages.push(value);
		else flags[key] = value;
	}
	return flags;
}

const flags = parseFlags(process.argv.slice(2));
if (!flags.socket || !flags['packages-dir']) {
	process.stderr.write('[sidecar-runner] --socket and --packages-dir are required\n');
	process.exit(2);
}

const role = flags.role === 'describe' ? 'describe' : 'run';
const tenant = typeof flags.tenant === 'string' ? flags.tenant : '';
const packagesDir = flags['packages-dir'];
const packageNames = [];
for (const name of flags.packages) {
	if (name !== '' && !packageNames.includes(name)) packageNames.push(name);
}

process.stderr.write(
	`[sidecar-runner] role=${role} tenant=${JSON.stringify(tenant)} packages=${packageNames.join(',')}\n`,
);

// --- the guard -------------------------------------------------------------

// The guard is a seat belt, not a sandbox. Node's permission model already
// denies the file system outside the granted paths and refuses child
// processes and native addons; it does not stop a package opening a socket to
// anywhere it can reach, and it does not stop a package signalling the host
// or another tenant's process (same uid, verified). So the runner refuses
// both, records every attempt, and fails the run afterwards even when the
// package swallowed the exception.
//
// Patch every route that reaches the network, not only net.Socket.prototype.connect:
// patching the socket alone leaves dns.lookup, dns.resolve4, dgram.send and a
// brand new dgram.Socket reaching the network untouched.
const violations = [];

function refusal(kind, route) {
	violations.push({ kind, route });
	const error = new Error(
		kind === 'signal'
			? `signalling another process is refused (${route})`
			: `network access is refused (${route}); use this.helpers.httpRequest so the host can apply its egress policy`,
	);
	error.code = kind === 'signal' ? 'KF_PROCESS_REFUSED' : 'KF_NETWORK_REFUSED';
	error.route = route;
	return error;
}

function thrower(kind, route) {
	return function refused() {
		throw refusal(kind, route);
	};
}

function patchMethod(holder, key, kind, route) {
	if (!holder || typeof holder[key] !== 'function') return;
	try {
		holder[key] = thrower(kind, route);
	} catch {
		// A frozen built-in is not a hole: the route is simply still there and
		// the run will be judged on what it actually did.
	}
}

function installNetworkGuard() {
	const dns = require('node:dns');
	const dgram = require('node:dgram');
	// Capture the resolver classes before patching, because they live on the
	// same object as the functions being replaced.
	const resolver = dns.Resolver;
	const promiseResolver = dns.promises.Resolver;

	patchMethod(net.Socket.prototype, 'connect', 'network', 'net.Socket.prototype.connect');
	patchMethod(net, 'connect', 'network', 'net.connect');
	patchMethod(net, 'createConnection', 'network', 'net.createConnection');
	patchMethod(net.Server.prototype, 'listen', 'network', 'net.Server.prototype.listen');

	for (const key of Object.keys(dns)) {
		if (key === 'Resolver' || key === 'promises') continue;
		patchMethod(dns, key, 'network', `dns.${key}`);
	}
	for (const key of Object.keys(dns.promises)) {
		if (key === 'Resolver') continue;
		patchMethod(dns.promises, key, 'network', `dns.promises.${key}`);
	}
	for (const key of Object.getOwnPropertyNames(resolver.prototype)) {
		patchMethod(resolver.prototype, key, 'network', `dns.Resolver.prototype.${key}`);
	}
	for (const key of Object.getOwnPropertyNames(promiseResolver.prototype)) {
		patchMethod(promiseResolver.prototype, key, 'network', `dns.promises.Resolver.prototype.${key}`);
	}

	patchMethod(dgram, 'createSocket', 'network', 'dgram.createSocket');
	for (const key of ['send', 'connect', 'bind']) {
		patchMethod(dgram.Socket.prototype, key, 'network', `dgram.Socket.prototype.${key}`);
	}

	for (const name of ['fetch', 'WebSocket', 'EventSource']) {
		if (typeof globalThis[name] !== 'function') continue;
		try {
			globalThis[name] = thrower('network', `global.${name}`);
		} catch {
			// Same reasoning as patchMethod: an unwritable global is not a hole.
		}
	}

	for (const name of ['kill', '_kill']) {
		const original = process[name];
		if (typeof original !== 'function') continue;
		process[name] = function guardedKill(pid, signal) {
			if (pid === process.pid) return original.call(process, pid, signal);
			throw refusal('signal', `process.${name}(${pid})`);
		};
	}
}

// --- package loading -------------------------------------------------------

const catalogue = [];
const nodeTable = new Map();

const FATAL_LOAD_CODES = new Set(['network-refused', 'process-refused', 'permission-denied', 'addon-disabled']);

// ERROR_CODES turns a Node or runner error code into the name the host reads.
const ERROR_CODES = {
	KF_UNSUPPORTED: 'unsupported',
	KF_NETWORK_REFUSED: 'network-refused',
	KF_PROCESS_REFUSED: 'process-refused',
	ERR_ACCESS_DENIED: 'permission-denied',
	ERR_DLOPEN_DISABLED: 'addon-disabled',
	MODULE_NOT_FOUND: 'missing-module',
};

function drainViolations() {
	return violations.splice(0, violations.length);
}

function violationCode(recorded) {
	return recorded.some((violation) => violation.kind === 'network') ? 'network-refused' : 'process-refused';
}

function loadIssue(packageName, file, code, message, severity) {
	return { package: packageName, file, code, message, severity };
}

function classifyLoadError(error) {
	const code = ERROR_CODES[error && error.code];
	if (code === 'missing-module') {
		const quoted = /'([^']+)'/.exec(String(error.message || ''));
		const missing = quoted ? quoted[1] : 'an unknown module';
		return {
			code,
			message:
				`this file needs the module ${JSON.stringify(missing)}, which is not installed: ` +
				'the operator must install the package\'s peer dependency beside it',
		};
	}
	if (code) return { code, message: String((error && error.message) || error) };
	return { code: 'load-failed', message: String((error && error.message) || error) };
}

function versionKey(version) {
	const numeric = Number(version);
	return Number.isFinite(numeric) ? String(numeric) : String(version);
}

function listVersions(version) {
	const list = Array.isArray(version) ? version : [version];
	const out = [];
	for (const entry of list) {
		if (entry === undefined || entry === null) continue;
		const key = versionKey(entry);
		if (!out.includes(key)) out.push(key);
	}
	return out;
}

function unsupportedMarkers(instance) {
	const markers = [];
	if (typeof instance.trigger === 'function') markers.push('trigger');
	if (typeof instance.poll === 'function') markers.push('poll');
	if (typeof instance.webhook === 'function' || Array.isArray(instance.webhook)) markers.push('webhook');
	if (instance.nodeVersions) markers.push('versioned-wrapper');
	return markers;
}

// pickClass finds the node (or credential) class a file exports. Hand-written
// packages and compiled ones both follow the same convention: the file
// basename names the class, and a file that exports exactly one class is
// accepted too.
function pickClass(moduleExports, relativePath, suffix) {
	if (typeof moduleExports === 'function') return moduleExports;
	if (!moduleExports || typeof moduleExports !== 'object') return null;
	const base = path.basename(relativePath).replace(/\.js$/, '').replace(new RegExp(`${suffix}$`), '');
	const named = moduleExports[base];
	if (typeof named === 'function') return named;
	const candidates = [];
	for (const key of Object.keys(moduleExports)) {
		if (key === '__esModule') continue;
		const value = moduleExports[key];
		if (typeof value === 'function' && value.prototype) candidates.push(value);
	}
	return candidates.length === 1 ? candidates[0] : null;
}

function insideDirectory(parent, child) {
	return child === parent || child.startsWith(parent + path.sep);
}

function findPackageDir(name) {
	for (const candidate of [path.join(packagesDir, 'node_modules', name), path.join(packagesDir, name)]) {
		try {
			if (fs.statSync(candidate).isDirectory()) return fs.realpathSync(candidate);
		} catch {
			// Not there; try the next location.
		}
	}
	return '';
}

// resolveManifestPath refuses a manifest entry that leaves the package. It
// checks the lexical path first — a `../` entry names a file that may not
// exist at all — and then the real path, so a symbolic link inside the
// package cannot point at a file outside it either.
function resolveManifestPath(entry, packageName, packageDir, relative) {
	const lexical = path.resolve(packageDir, relative);
	if (!insideDirectory(packageDir, lexical)) {
		entry.errors.push(
			loadIssue(packageName, relative, 'path-escape', `the manifest path ${JSON.stringify(relative)} resolves outside the package directory`, 'fatal'),
		);
		return null;
	}
	let real = '';
	try {
		real = fs.realpathSync(lexical);
	} catch (error) {
		entry.errors.push(
			loadIssue(packageName, relative, 'file-missing', `the manifest path ${JSON.stringify(relative)} does not exist: ${error.message}`, 'fatal'),
		);
		return null;
	}
	if (!insideDirectory(packageDir, real)) {
		entry.errors.push(
			loadIssue(packageName, relative, 'symlink-escape', `the manifest path ${JSON.stringify(relative)} leaves the package directory through a symbolic link`, 'fatal'),
		);
		return null;
	}
	return { file: relative, real };
}

function loadNodeFile(entry, file) {
	drainViolations();
	let moduleExports;
	try {
		moduleExports = require(file.real);
	} catch (error) {
		const recorded = drainViolations();
		const classified = recorded.length
			? { code: violationCode(recorded), message: `loading this file attempted ${recorded[0].route}` }
			: classifyLoadError(error);
		entry.errors.push(loadIssue(entry.name, file.file, classified.code, classified.message, FATAL_LOAD_CODES.has(classified.code) ? 'fatal' : 'file'));
		return;
	}
	const recorded = drainViolations();
	if (recorded.length) {
		entry.errors.push(loadIssue(entry.name, file.file, violationCode(recorded), `loading this file attempted ${recorded[0].route}`, 'fatal'));
		return;
	}

	const NodeClass = pickClass(moduleExports, file.file, '.node');
	if (!NodeClass) {
		entry.errors.push(loadIssue(entry.name, file.file, 'no-node-class', 'the file exports no node class; export one class, or name it after the file', 'file'));
		return;
	}
	let instance;
	try {
		instance = new NodeClass();
	} catch (error) {
		entry.errors.push(loadIssue(entry.name, file.file, 'construct-failed', `the node class could not be constructed: ${error.message}`, 'file'));
		return;
	}
	const description = instance && instance.description;
	if (!description || typeof description !== 'object') {
		entry.errors.push(loadIssue(entry.name, file.file, 'no-description', 'the node class carries no description object', 'file'));
		return;
	}
	if (typeof description.name !== 'string' || description.name === '') {
		entry.errors.push(loadIssue(entry.name, file.file, 'no-name', 'the node description carries no name', 'file'));
		return;
	}

	const versions = listVersions(description.version);
	for (const version of versions) {
		const key = `${description.name}@${version}`;
		const owner = nodeTable.get(key);
		if (owner) {
			entry.errors.push(
				loadIssue(entry.name, file.file, 'duplicate-node', `node ${description.name} version ${version} is also declared in ${owner.file}`, 'file'),
			);
			continue;
		}
		const node = {
			file: file.file,
			name: description.name,
			// The version exactly as the package declared it: a number, or an
			// array when one file carries several. The dispatch key is
			// normalised from it, so the catalogue stays a faithful record.
			version: description.version,
			execute: typeof instance.execute === 'function',
			unsupported: unsupportedMarkers(instance),
			description,
		};
		nodeTable.set(key, { instance, node, file: file.file });
		entry.nodes.push(node);
	}
}

function loadCredentialFile(entry, file) {
	drainViolations();
	let moduleExports;
	try {
		moduleExports = require(file.real);
	} catch (error) {
		const classified = classifyLoadError(error);
		entry.errors.push(loadIssue(entry.name, file.file, classified.code, classified.message, FATAL_LOAD_CODES.has(classified.code) ? 'fatal' : 'file'));
		return;
	}
	const recorded = drainViolations();
	if (recorded.length) {
		entry.errors.push(loadIssue(entry.name, file.file, violationCode(recorded), `loading this file attempted ${recorded[0].route}`, 'fatal'));
		return;
	}
	const CredentialClass = pickClass(moduleExports, file.file, '.credentials');
	if (!CredentialClass) {
		entry.errors.push(loadIssue(entry.name, file.file, 'no-credential-class', 'the file exports no credential class', 'file'));
		return;
	}
	let instance;
	try {
		instance = new CredentialClass();
	} catch (error) {
		entry.errors.push(loadIssue(entry.name, file.file, 'construct-failed', `the credential class could not be constructed: ${error.message}`, 'file'));
		return;
	}
	if (!instance || typeof instance.name !== 'string') {
		entry.errors.push(loadIssue(entry.name, file.file, 'no-credential-name', 'the credential class carries no name', 'file'));
		return;
	}
	entry.credentials.push({
		file: file.file,
		name: instance.name,
		displayName: instance.displayName,
		documentationUrl: instance.documentationUrl,
		properties: instance.properties,
	});
}

function loadPackage(name) {
	const entry = { name, version: '', nodes: [], credentials: [], errors: [] };
	const packageDir = findPackageDir(name);
	if (!packageDir) {
		entry.errors.push(
			loadIssue(name, '', 'package-not-found', `no package named ${JSON.stringify(name)} was found in node_modules/ or at the top level of the packages directory`, 'fatal'),
		);
		return entry;
	}

	let manifest;
	try {
		manifest = JSON.parse(fs.readFileSync(path.join(packageDir, 'package.json'), 'utf8'));
	} catch (error) {
		entry.errors.push(loadIssue(name, 'package.json', 'manifest-unreadable', `the package manifest could not be read: ${error.message}`, 'fatal'));
		return entry;
	}
	if (typeof manifest.version === 'string') entry.version = manifest.version;

	const manifestInfo = manifest.n8n;
	if (!manifestInfo || typeof manifestInfo !== 'object') {
		entry.errors.push(loadIssue(name, 'package.json', 'manifest-missing', 'the manifest declares no "n8n" section, so it holds no nodes this sidecar can load', 'fatal'));
		return entry;
	}
	if (Number(manifestInfo.n8nNodesApiVersion) !== 1) {
		entry.errors.push(
			loadIssue(
				name,
				'package.json',
				'manifest-api-version',
				`n8n.n8nNodesApiVersion is ${JSON.stringify(manifestInfo.n8nNodesApiVersion)}, and this sidecar loads version 1 only`,
				'fatal',
			),
		);
	}

	const nodeFiles = [];
	for (const relative of Array.isArray(manifestInfo.nodes) ? manifestInfo.nodes : []) {
		const resolved = resolveManifestPath(entry, name, packageDir, String(relative));
		if (resolved) nodeFiles.push(resolved);
	}
	const credentialFiles = [];
	for (const relative of Array.isArray(manifestInfo.credentials) ? manifestInfo.credentials : []) {
		const resolved = resolveManifestPath(entry, name, packageDir, String(relative));
		if (resolved) credentialFiles.push(resolved);
	}

	if (entry.errors.some((issue) => issue.severity === 'fatal')) return entry;

	for (const file of nodeFiles) loadNodeFile(entry, file);
	for (const file of credentialFiles) loadCredentialFile(entry, file);
	return entry;
}

function loadPackages() {
	for (const name of packageNames) catalogue.push(loadPackage(name));
}

// --- the host call channel -------------------------------------------------

const HTTP_OPTIONS = new Set(['method', 'url', 'headers', 'qs', 'body', 'json', 'timeout', 'returnFullResponse', 'ignoreHttpStatusErrors']);

let callCounter = 0;
const pendingCalls = new Map();

function unsupported(name) {
	const error = new Error(`${name} is not implemented by the KilasFlow JavaScript sidecar`);
	error.code = 'KF_UNSUPPORTED';
	error.member = name;
	return error;
}

function withQueryString(url, qs) {
	if (!qs || typeof qs !== 'object') return url;
	const parsed = new URL(url);
	for (const [key, value] of Object.entries(qs)) {
		if (value === undefined || value === null) continue;
		if (Array.isArray(value)) {
			for (const entry of value) parsed.searchParams.append(key, String(entry));
		} else {
			parsed.searchParams.append(key, String(value));
		}
	}
	return parsed.toString();
}

function headerValue(headers, name) {
	const wanted = name.toLowerCase();
	for (const key of Object.keys(headers)) {
		if (key.toLowerCase() === wanted) return headers[key];
	}
	return '';
}

// httpRequest is deliberately not an async function: an option this sidecar
// does not implement throws synchronously, so a package that calls it without
// awaiting still gets a failure the run can report instead of an unhandled
// rejection that takes the process down.
function httpRequest(runId, options) {
	if (options === null || typeof options !== 'object') {
		throw unsupported('helpers.httpRequest called without an options object');
	}
	for (const key of Object.keys(options)) {
		// Never ignore an option: `auth`, `proxy` and
		// `skipSslCertificateValidation` change the security posture, and a
		// silently dropped one is a boundary wider than the caller believes.
		if (!HTTP_OPTIONS.has(key)) throw unsupported(`helpers.httpRequest option ${JSON.stringify(key)}`);
	}
	if (typeof options.url !== 'string' || options.url === '') {
		throw new Error('helpers.httpRequest needs a url');
	}

	const headers = {};
	if (options.headers && typeof options.headers === 'object') {
		for (const [key, value] of Object.entries(options.headers)) {
			if (value === undefined || value === null) continue;
			headers[key] = String(value);
		}
	}

	let body = '';
	if (options.body !== undefined && options.body !== null) {
		if (Buffer.isBuffer(options.body)) body = Buffer.from(options.body);
		else if (typeof options.body === 'string') body = options.body;
		else {
			body = JSON.stringify(options.body);
			if (!headerValue(headers, 'content-type')) headers['Content-Type'] = 'application/json';
		}
	}
	if (options.json === true && body !== '' && !headerValue(headers, 'content-type')) {
		headers['Content-Type'] = 'application/json';
	}

	const call = `call-${++callCounter}`;
	const timeoutMs = Number.isFinite(Number(options.timeout)) ? Number(options.timeout) : 0;
	const answer = new Promise((resolve, reject) => {
		pendingCalls.set(call, { runId, resolve, reject, method: String(options.method || 'GET'), url: String(options.url) });
	});
	const line = {
		type: 'http.request',
		id: runId,
		call,
		method: String(options.method || 'GET').toUpperCase(),
		url: withQueryString(options.url, options.qs),
		headers,
		bodyBase64: body === '' ? '' : Buffer.from(body).toString('base64'),
		timeoutMs,
	};
	send(line);

	return answer.then((response) => interpretResponse(options, response));
}

function interpretResponse(options, response) {
	const headers = response.headers || {};
	const status = Number(response.status) || 0;
	const text = response.bodyBase64 ? Buffer.from(response.bodyBase64, 'base64').toString('utf8') : '';
	const wantsJson = options.json === true || headerValue(headers, 'content-type').includes('json');

	let data = text === '' ? undefined : text;
	if (wantsJson && text !== '') {
		try {
			data = JSON.parse(text);
		} catch {
			throw new Error(`helpers.httpRequest could not parse the response from ${options.url} as JSON`);
		}
	}

	if (status >= 400 && options.ignoreHttpStatusErrors !== true) {
		const error = new Error(`Request failed with status code ${status}`);
		error.httpCode = status;
		error.statusCode = status;
		error.response = { statusCode: status, statusMessage: response.statusText || '', headers, body: data };
		throw error;
	}
	if (options.returnFullResponse === true) {
		return { body: data, headers, statusCode: status, statusMessage: response.statusText || '' };
	}
	return data;
}

function settleHostCall(frame) {
	const call = pendingCalls.get(frame.call);
	if (!call) return;
	pendingCalls.delete(frame.call);
	if (frame.type === 'http.error') {
		const error = new Error(String(frame.message || 'the host refused the request'));
		error.code = frame.code || 'http-error';
		call.reject(error);
		return;
	}
	call.resolve(frame);
}

// abandonHostCalls drops a finished run's pending calls so its parameters and
// credentials stop being reachable from the call channel.
function abandonHostCalls(runId) {
	for (const [call, pending] of pendingCalls) {
		if (pending.runId !== runId) continue;
		pendingCalls.delete(call);
		pending.reject(new Error('the run ended before the host answered its request'));
	}
}

// --- the node-facing surface ----------------------------------------------

function readDotted(source, dotted) {
	let value = source;
	for (const segment of String(dotted).split('.')) {
		if (value === null || value === undefined || typeof value !== 'object') return undefined;
		value = value[segment];
	}
	return value;
}

// returnJsonArray and constructExecutionMetaData are the two item helpers a
// package needs, and the names of the helpers this sidecar does not implement
// are refused by name rather than by the generic member error.
function returnJsonArray(data) {
	const list = Array.isArray(data) ? data : [data];
	return list.map((item) => (item && typeof item === 'object' && item.json !== undefined ? item : { json: item }));
}

function constructExecutionMetaData(data, options) {
	const items = returnJsonArray(data);
	const itemData = options && options.itemData;
	if (!itemData || itemData.item === undefined) return items;
	return items.map((item) => Object.assign({}, item, { pairedItem: { item: itemData.item } }));
}

function unimplementedHelper(name) {
	return function refusedHelper() {
		throw unsupported(`helpers.${name}`);
	};
}

function buildHelpers(runId) {
	return memberProxy(
		{
			httpRequest: (options) => httpRequest(runId, options),
			returnJsonArray,
			constructExecutionMetaData,
			// Real members of the other runtime's helpers. Refusing them by
			// name is more useful than the generic unknown-member error, and
			// a community credential's `authenticate` block is ignored in v1,
			// so a package that calls one must be told rather than quietly
			// sending nothing.
			httpRequestWithAuthentication: unimplementedHelper('httpRequestWithAuthentication'),
			requestWithAuthentication: unimplementedHelper('requestWithAuthentication'),
			request: unimplementedHelper('request'),
		},
		'helpers',
	);
}

// buildFunctions is everything a package may reach on `this`. `run` is the
// frame being served, so the surface holds that run's own items, parameters
// and credentials and nothing else.
function buildFunctions(run, emit) {
	const frame = run.frame;
	const paramsFor = (itemIndex) => {
		if (Array.isArray(frame.paramsByItem) && frame.paramsByItem.length > 0) {
			return frame.paramsByItem[itemIndex] || {};
		}
		return frame.params || {};
	};
	const credentials = Object.assign({}, frame.credentials || {}, frame.secrets || {});

	return {
		getInputData() {
			return run.items.map((item) => ({ json: item.json }));
		},
		getNodeParameter(name, itemIndex, fallback) {
			const value = readDotted(paramsFor(itemIndex), name);
			if (value === undefined) {
				if (arguments.length >= 3) return fallback;
				throw new Error(`Could not get parameter "${name}"`);
			}
			return value;
		},
		getCredentials(type) {
			const value = credentials[type];
			if (value === undefined) throw new Error(`Could not get credentials for "${type}"`);
			return Promise.resolve(value);
		},
		getNode() {
			const context = frame.context || {};
			return {
				id: context.nodeName || '',
				name: context.nodeName || '',
				type: context.nodeType || '',
				typeVersion: context.typeVersion,
				position: [0, 0],
				parameters: frame.params || {},
			};
		},
		getWorkflow() {
			// Only the identity the host knows is populated: a community node
			// never receives the whole workflow document.
			return { id: (frame.context && frame.context.workflowId) || '', name: '', active: true };
		},
		getExecutionId() {
			return (frame.context && frame.context.executionId) || '';
		},
		getMode() {
			return (frame.context && frame.context.mode) || 'manual';
		},
		getTimezone() {
			return (frame.context && frame.context.timezone) || 'UTC';
		},
		continueOnFail() {
			// Always false: the engine's per-item tolerance decides, never the
			// package.
			return false;
		},
		logger: {
			debug: emit('debug'),
			info: emit('info'),
			warn: emit('warn'),
			error: emit('error'),
		},
		helpers: buildHelpers(frame.id),
	};
}

// memberProxy is what an unimplemented member hits: it names the member
// instead of returning undefined, which turns a silent no-op into a
// diagnostic. Symbols and the probes `await` and loggers make are exempt, so
// awaiting or inspecting the surface can never throw.
const PROMISE_PROBES = new Set(['then', 'catch', 'finally']);
const INSPECT_PROBES = new Set(['toJSON', 'inspect', 'constructor', 'toString', 'valueOf', 'name', 'length']);

function memberProxy(target, prefix) {
	return new Proxy(target, {
		get(inner, property, receiver) {
			if (typeof property === 'symbol') return Reflect.get(inner, property, receiver);
			if (property in inner) return inner[property];
			if (PROMISE_PROBES.has(property)) return undefined;
			if (INSPECT_PROBES.has(property)) return Reflect.get(inner, property, receiver);
			throw unsupported(`${prefix}.${String(property)}`);
		},
	});
}

// --- the protocol ----------------------------------------------------------

function send(frame) {
	let line;
	try {
		line = JSON.stringify(frame);
	} catch (error) {
		line = JSON.stringify({ type: 'error', id: frame && frame.id, code: 'unserialisable', message: `the run produced a value that cannot be sent: ${error.message}` });
	}
	if (socket.destroyed) return;
	socket.write(`${line}\n`);
}

function violationAnswer(id, recorded) {
	const network = recorded.find((violation) => violation.kind === 'network');
	if (network) {
		return {
			type: 'error',
			id,
			code: 'network-refused',
			message:
				`the package tried to reach the network through ${network.route}; community nodes must use ` +
				'this.helpers.httpRequest so the host applies its egress policy',
		};
	}
	const signal = recorded.find((violation) => violation.kind === 'signal');
	return {
		type: 'error',
		id,
		code: 'process-refused',
		message: `${signal ? signal.route : 'a signal'} was refused: a sidecar package may not signal the host or another tenant's process`,
	};
}

function failureAnswer(id, error) {
	const code = ERROR_CODES[error && error.code] || (error instanceof Error ? 'node-error' : 'node-threw');
	const message = String((error && error.message) || error);
	return { type: 'error', id, code, message };
}

function nodeFailure(message) {
	const error = new Error(message);
	error.code = 'bad-output';
	return error;
}

function normaliseOutputs(produced) {
	if (produced === undefined || produced === null) throw nodeFailure('the node returned no output');
	if (!Array.isArray(produced) || produced.length === 0) {
		throw nodeFailure('the node returned no output port: execute() must resolve to an array with one entry per output');
	}
	const outputs = [];
	for (let portIndex = 0; portIndex < produced.length; portIndex++) {
		const port = produced[portIndex];
		if (!Array.isArray(port)) throw nodeFailure(`output port ${portIndex} is not an array of items`);
		const items = [];
		for (let itemIndex = 0; itemIndex < port.length; itemIndex++) {
			const item = port[itemIndex];
			if (!item || typeof item !== 'object' || !item.json || typeof item.json !== 'object' || Array.isArray(item.json)) {
				throw nodeFailure(`output item ${itemIndex} of port ${portIndex} carries no json object`);
			}
			if (item.binary !== undefined && item.binary !== null) {
				throw nodeFailure(`output item ${itemIndex} of port ${portIndex} carries binary data, which this sidecar refuses`);
			}
			const normalised = { json: item.json };
			if (item.pairedItem !== undefined) normalised.pairedItem = item.pairedItem;
			items.push(normalised);
		}
		outputs.push(items);
	}
	return outputs;
}

async function handleExecute(frame) {
	const runId = frame.id;
	try {
		if (String(frame.tenant === undefined ? '' : frame.tenant) !== tenant) {
			return {
				type: 'error',
				id: runId,
				code: 'tenant-mismatch',
				message: `this process serves tenant ${JSON.stringify(tenant)} and was asked to run a frame for ${JSON.stringify(frame.tenant)}`,
			};
		}
		const key = `${frame.node}@${versionKey(frame.nodeVersion)}`;
		const found = nodeTable.get(key);
		if (!found) {
			const available = [...nodeTable.keys()].join(', ') || 'none';
			return {
				type: 'error',
				id: runId,
				code: 'unknown-node',
				message: `no node ${key} is loaded; the packages on disk may have changed since this process was started (loaded: ${available})`,
			};
		}
		if (!found.node.execute) {
			return {
				type: 'error',
				id: runId,
				code: 'not-executable',
				message: `node ${key} has no execute() and cannot run in a workflow`,
			};
		}

		const items = Array.isArray(frame.items) ? frame.items : [];
		for (const item of items) {
			if (item && item.binary !== undefined && item.binary !== null) {
				return {
					type: 'error',
					id: runId,
					code: 'binary-unsupported',
					message: 'this input item carries binary data, which this sidecar refuses',
				};
			}
		}

		const run = { frame, items };
		drainViolations();
		const sandbox = memberProxy(
			buildFunctions(run, (level) => (message) => {
				process.stderr.write(`[sidecar-runner ${level}] ${String(message)}\n`);
			}),
			'this',
		);

		let failure = null;
		let outputs = null;
		try {
			outputs = normaliseOutputs(await found.instance.execute.call(sandbox));
		} catch (error) {
			failure = error;
		}

		// A package that tried to escape and swallowed the exception still
		// fails the run: the record, not the exception, is the evidence.
		const recorded = drainViolations();
		if (recorded.length) return violationAnswer(runId, recorded);
		if (failure) return failureAnswer(runId, failure);
		return { type: 'result', id: runId, outputs };
	} finally {
		abandonHostCalls(runId);
		drainViolations();
		if (frame && typeof frame === 'object') {
			// Best effort: the host's own request object is out of reach from
			// here, so this only drops the runner's references to it.
			frame.params = null;
			frame.paramsByItem = null;
			frame.credentials = null;
			frame.secrets = null;
			frame.items = null;
		}
	}
}

function dispatch(line) {
	let frame;
	try {
		frame = JSON.parse(line);
	} catch {
		// The host frames every message as one JSON line, so this cannot
		// happen; refusing to die on it is cheaper than a crash loop.
		return;
	}
	if (frame.type === 'http.response' || frame.type === 'http.error') {
		settleHostCall(frame);
		return;
	}
	if (frame.type === 'describe') {
		send({ type: 'result', id: frame.id, catalogue: { nodeVersion: process.versions.node, packages: catalogue } });
		return;
	}
	if (frame.type === 'execute') {
		if (running) {
			send({ type: 'error', id: frame.id, code: 'run-in-progress', message: 'the runner is already executing a node' });
			return;
		}
		running = true;
		handleExecute(frame)
			.then((answer) => send(answer))
			.catch((error) => send(failureAnswer(frame.id, error)))
			.finally(() => {
				running = false;
			});
		return;
	}
	send({ type: 'error', id: frame.id, code: 'unknown-frame', message: `no such frame: ${String(frame.type)}` });
}

// --- startup --------------------------------------------------------------

// Dial before hardening: the guard replaces the very connect path the host
// socket needs, and the socket must exist before a package can be loaded
// through it.
const socket = net.createConnection(flags.socket);
socket.setEncoding('utf8');
socket.setNoDelay(true);

let running = false;
let buffer = '';

socket.on('close', () => {
	// The host owns this process's lifetime: an orphan that outlived it could
	// never be reached, so it leaves with the socket.
	process.exit(0);
});
socket.on('error', (error) => {
	process.stderr.write(`[sidecar-runner] socket error: ${error.message}\n`);
	process.exit(1);
});

installNetworkGuard();
loadPackages();

socket.on('data', (chunk) => {
	buffer += chunk;
	for (;;) {
		const end = buffer.indexOf('\n');
		if (end < 0) break;
		const line = buffer.slice(0, end);
		buffer = buffer.slice(end + 1);
		if (line === '') continue;
		dispatch(line);
	}
});
