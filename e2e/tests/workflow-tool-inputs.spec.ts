import { createServer, type Server } from 'node:http';
import { once } from 'node:events';
import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures';
import { startServer } from '../helpers/server';
import { createWorkflow, runWorkflow, waitForExecution } from '../helpers/seed';

// The Workflow Tool's inputs as a mapper with a per-field model/fixed switch
// (BUG-n6p7qy): the node panel lists the picked workflow's declared fields,
// each one the model's or fixed, and a run offers the model exactly the model's
// columns while the sub-workflow receives the fixed ones.

interface ScriptedProvider {
	port: number;
	/** The tool schemas the first model turn was offered, by tool name. */
	offered: () => Record<string, any>;
	/** The messages the second model turn carried, which hold the tool result. */
	secondTurnMessages: () => any[];
	close: () => Promise<void>;
}

/**
 * An OpenAI-compatible endpoint that calls the tool once and then answers.
 *
 * Its own server rather than the shared stub: the run's outbound policy admits
 * one loopback endpoint per instance, and a scripted model is the only thing
 * that can make an agent call a tool deterministically.
 */
async function startProvider(toolName: string, toolArguments: string): Promise<ScriptedProvider> {
	const offered: Record<string, any> = {};
	let secondTurn: any[] = [];
	let turn = 0;
	const server: Server = createServer((request, response) => {
		const chunks: Buffer[] = [];
		request.on('data', (chunk: Buffer) => chunks.push(chunk));
		request.on('end', () => {
			const body = JSON.parse(Buffer.concat(chunks).toString('utf-8') || '{}');
			response.writeHead(200, { 'content-type': 'application/json' });
			if (turn === 0) {
				for (const tool of body.tools ?? []) offered[tool.function.name] = tool.function.parameters;
				turn++;
				response.end(
					JSON.stringify({
						choices: [
							{
								message: {
									role: 'assistant',
									tool_calls: [{ id: 'call_1', type: 'function', function: { name: toolName, arguments: toolArguments } }]
								},
								finish_reason: 'tool_calls'
							}
						],
						usage: { total_tokens: 10 }
					})
				);
				return;
			}
			secondTurn = body.messages ?? [];
			response.end(
				JSON.stringify({
					choices: [{ message: { role: 'assistant', content: '98.6F' }, finish_reason: 'stop' }],
					usage: { total_tokens: 5 }
				})
			);
		});
	});
	server.listen(0, '127.0.0.1');
	await once(server, 'listening');
	const address = server.address();
	if (!address || typeof address === 'string') throw new Error('unable to reserve a port for the scripted model');
	return {
		port: address.port,
		offered: () => offered,
		secondTurnMessages: () => secondTurn,
		close: async () => {
			server.close();
			await once(server, 'close');
		}
	};
}

async function api(baseURL: string, method: string, path: string, body?: unknown, want = 200): Promise<any> {
	const response = await fetch(`${baseURL}/api/v1${path}`, {
		method,
		headers: { 'content-type': 'application/json' },
		body: body === undefined ? undefined : JSON.stringify(body)
	});
	if (response.status !== want) {
		throw new Error(`${method} ${path}: status ${response.status}, want ${want} (body: ${await response.text()})`);
	}
	return response.status === 204 ? null : response.json();
}

function toolSwitch(page: Page, column: string) {
	return page.getByRole('switch', { name: `${column}: defined by the model` });
}

test('a Workflow Tool lists the workflow’s fields, each the model’s or fixed, and a run honours both', async ({ page }) => {
	const provider = await startProvider('celsius_to_fahrenheit', '{"tempC":37}');
	const server = await startServer({ stubEndpoint: `127.0.0.1:${provider.port}` });
	try {
		const child = await createWorkflow(server.baseURL, {
			schemaVersion: 1,
			name: 'Celsius to Fahrenheit',
			nodes: [
				{
					id: 'start',
					name: 'When Executed by Another Workflow',
					type: 'kilasflow.executeWorkflowTrigger',
					typeVersion: 1,
					position: { x: 0, y: 0 },
					parameters: {
						inputSource: 'fields',
						workflowInputs: {
							values: [
								{ name: 'tempC', type: 'number' },
								{ name: 'roundTo', type: 'number' }
							]
						}
					}
				},
				{
					id: 'echo',
					name: 'Echo',
					type: 'kilasflow.set',
					typeVersion: 1,
					position: { x: 240, y: 0 },
					parameters: {
						assignments: {
							gotTempC: { mode: 'expression', value: '{{ $json.tempC }}' },
							gotRoundTo: { mode: 'expression', value: '{{ $json.roundTo }}' }
						}
					}
				}
			],
			connections: [{ id: 'c1', kind: 'main', source: { nodeId: 'start', port: 'main' }, target: { nodeId: 'echo', port: 'main' } }],
			settings: {}
		});
		await api(server.baseURL, 'POST', `/workflows/${child.id}/activate`);

		const parent = await createWorkflow(server.baseURL, {
			schemaVersion: 1,
			name: 'Agent With A Tool',
			nodes: [
				{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
				{
					id: 'model',
					name: 'Model',
					type: 'kilasflow.chatModel',
					typeVersion: 1,
					position: { x: 0, y: 240 },
					// One JSON body per turn is what the scripted endpoint serves.
					parameters: { model: 'scripted', baseUrl: `http://127.0.0.1:${provider.port}`, stream: false }
				},
				{ id: 'agent', name: 'Agent', type: 'kilasflow.agent', typeVersion: 1, position: { x: 240, y: 0 }, parameters: { prompt: 'Convert 37 C.' } },
				{
					id: 'tool',
					name: 'Convert',
					type: 'kilasflow.workflowTool',
					typeVersion: 1,
					position: { x: 240, y: 240 },
					parameters: {
						toolName: 'celsius_to_fahrenheit',
						toolDescription: 'Converts Celsius to Fahrenheit.',
						workflowId: { __rl: true, mode: 'list', value: child.id }
					}
				}
			],
			connections: [
				{ id: 'c1', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'agent', port: 'main' } },
				{ id: 'c2', kind: 'ai_languageModel', source: { nodeId: 'model', port: 'model' }, target: { nodeId: 'agent', port: 'model' } },
				{ id: 'c3', kind: 'ai_tool', source: { nodeId: 'tool', port: 'tool' }, target: { nodeId: 'agent', port: 'tools' } }
			],
			settings: {}
		});

		// The column list is the picked workflow's own: names and types from
		// its trigger, every column the model's until the author fixes one.
		const loaded = await api(server.baseURL, 'POST', '/node-types/kilasflow.workflowTool/load-schema', {
			property: 'workflowInputs',
			parameters: { workflowId: { __rl: true, mode: 'list', value: child.id } }
		});
		expect(loaded.fields.map((field: any) => `${field.id}:${field.type}`)).toEqual(['tempC:number', 'roundTo:number']);

		await page.goto(`${server.baseURL}/app/workflows/${parent.id}`);
		await expect(page.locator('[data-testid="workflow-canvas"]')).toBeVisible();
		await page.locator('[data-testid="workflow-canvas"] .svelte-flow__node').filter({ hasText: 'Convert' }).first().click();
		const panel = page.getByRole('region', { name: 'Convert properties' });
		await expect(panel).toBeVisible();
		await panel.getByRole('tab', { name: 'Parameters' }).click();

		await expect(toolSwitch(panel, 'tempC')).toHaveAttribute('aria-checked', 'true');
		await expect(toolSwitch(panel, 'roundTo')).toHaveAttribute('aria-checked', 'true');
		await expect(panel.getByText('The model fills this in when it calls the tool.').first()).toBeVisible();

		// Fix roundTo to 1. tempC goes to fixed and back, which is the one way
		// to see the model's own stored form: a whole $fromAI call.
		await toolSwitch(panel, 'roundTo').click();
		await expect(toolSwitch(panel, 'roundTo')).toHaveAttribute('aria-checked', 'false');
		await panel.getByRole('textbox', { name: 'roundTo', exact: true }).fill('1');
		await toolSwitch(panel, 'tempC').click();
		await expect(toolSwitch(panel, 'tempC')).toHaveAttribute('aria-checked', 'false');
		await toolSwitch(panel, 'tempC').click();
		await expect(toolSwitch(panel, 'tempC')).toHaveAttribute('aria-checked', 'true');

		const save = page.getByRole('button', { name: 'Save', exact: true });
		await expect(save).toBeEnabled();
		await save.click();
		await expect(save).toBeDisabled();

		const stored = (await api(server.baseURL, 'GET', `/workflows/${parent.id}`)).latestVersion.document;
		const mapper = stored.nodes.find((entry: any) => entry.id === 'tool').parameters.workflowInputs;
		expect(mapper.mappingMode).toBe('defineBelow');
		expect(mapper.value.roundTo).toBe(1);
		expect(mapper.value.tempC).toEqual({ mode: 'expression', value: "{{ $fromAI('tempC', '', 'number') }}" });
		expect(mapper.schema.map((column: any) => `${column.id}:${column.type}`)).toEqual(['tempC:number', 'roundTo:number']);

		// The model is offered exactly its column, typed, and the sub-workflow
		// receives the model's value and the fixed one.
		const executionId = await runWorkflow(server.baseURL, parent.id);
		const record = await waitForExecution(server.baseURL, executionId);
		expect(record.status).toBe('succeeded');
		const schema = provider.offered().celsius_to_fahrenheit;
		expect(Object.keys(schema.properties)).toEqual(['tempC']);
		expect(schema.properties.tempC.type).toBe('number');
		expect(schema.required).toEqual(['tempC']);
		const toolResult = provider.secondTurnMessages().find((message) => message.role === 'tool');
		expect(toolResult?.content).toMatch(/"gotTempC":\s*37/);
		expect(toolResult?.content).toMatch(/"gotRoundTo":\s*1\b/);

		// And the n8n export marks the model's column the way n8n does, then
		// imports back with the same columns.
		const exported = await api(server.baseURL, 'GET', `/workflows/${parent.id}/export?format=n8n`);
		const exportedTool = exported.workflow.nodes.find((entry: any) => entry.name === 'Convert');
		expect(exportedTool.parameters.workflowInputs.value.tempC).toMatch(/^=\{\{ \/\*n8n-auto-generated-fromAI-override\*\/ \$fromAI\('tempC'/);
		expect(exportedTool.parameters.workflowInputs.value.roundTo).toBe(1);
		const imported = await api(server.baseURL, 'POST', '/workflows/import', { format: 'n8n', workflow: exported.workflow, name: 'Round trip' }, 201);
		const again = (await api(server.baseURL, 'GET', `/workflows/${imported.workflow.id}`)).latestVersion.document;
		const reimported = again.nodes.find((entry: any) => entry.name === 'Convert').parameters.workflowInputs;
		expect(reimported.value.roundTo).toBe(1);
		expect(reimported.value.tempC.mode).toBe('expression');
		expect(reimported.value.tempC.value).toContain("$fromAI('tempC'");
	} finally {
		await server.close();
		await provider.close();
	}
});

/** A parent workflow whose one tool carries the stored inputs given, and its sub-workflow. */
async function seedToolWith(
	baseURL: string,
	declared: Array<{ name: string; type: string }>,
	workflowInputs: unknown
): Promise<{ parentId: string }> {
	const child = await createWorkflow(baseURL, {
		schemaVersion: 1,
		name: 'Sub-workflow',
		nodes: [
			{
				id: 'start',
				name: 'When Executed by Another Workflow',
				type: 'kilasflow.executeWorkflowTrigger',
				typeVersion: 1,
				position: { x: 0, y: 0 },
				parameters: declared.length > 0 ? { inputSource: 'fields', workflowInputs: { values: declared } } : { inputSource: 'passthrough' }
			}
		],
		connections: [],
		settings: {}
	});
	await api(baseURL, 'POST', `/workflows/${child.id}/activate`);
	const parent = await createWorkflow(baseURL, {
		schemaVersion: 1,
		name: 'Agent With A Tool',
		nodes: [
			{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
			{ id: 'model', name: 'Model', type: 'kilasflow.chatModel', typeVersion: 1, position: { x: 0, y: 240 }, parameters: { model: 'scripted', baseUrl: 'http://127.0.0.1:9' } },
			{ id: 'agent', name: 'Agent', type: 'kilasflow.agent', typeVersion: 1, position: { x: 240, y: 0 }, parameters: { prompt: 'Convert.' } },
			{
				id: 'tool',
				name: 'Convert',
				type: 'kilasflow.workflowTool',
				typeVersion: 1,
				position: { x: 240, y: 240 },
				parameters: {
					toolName: 'convert',
					toolDescription: 'Converts.',
					workflowId: { __rl: true, mode: 'list', value: child.id },
					workflowInputs
				}
			}
		],
		connections: [
			{ id: 'c1', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'agent', port: 'main' } },
			{ id: 'c2', kind: 'ai_languageModel', source: { nodeId: 'model', port: 'model' }, target: { nodeId: 'agent', port: 'model' } },
			{ id: 'c3', kind: 'ai_tool', source: { nodeId: 'tool', port: 'tool' }, target: { nodeId: 'agent', port: 'tools' } }
		],
		settings: {}
	});
	return { parentId: parent.id };
}

async function openToolPanel(page: Page, baseURL: string, parentId: string) {
	await page.goto(`${baseURL}/app/workflows/${parentId}`);
	await expect(page.locator('[data-testid="workflow-canvas"]')).toBeVisible();
	await page.locator('[data-testid="workflow-canvas"] .svelte-flow__node').filter({ hasText: 'Convert' }).first().click();
	const panel = page.getByRole('region', { name: 'Convert properties' });
	await expect(panel).toBeVisible();
	await panel.getByRole('tab', { name: 'Parameters' }).click();
	return panel;
}

async function storedInputs(baseURL: string, parentId: string): Promise<any> {
	const document = (await api(baseURL, 'GET', `/workflows/${parentId}`)).latestVersion.document;
	return document.nodes.find((entry: any) => entry.id === 'tool').parameters.workflowInputs;
}

test('a tool whose inputs are one expression keeps it until the author confirms the per-field form', async ({ page, server }) => {
	const expression = { mode: 'expression', value: '{{ { "unit": "F" } }}' };
	const { parentId } = await seedToolWith(server.baseURL, [{ name: 'tempC', type: 'number' }], expression);
	const panel = await openToolPanel(page, server.baseURL, parentId);

	await expect(panel.getByText('These inputs are written as one expression')).toBeVisible();
	await expect(panel.getByRole('switch', { name: /defined by the model/ })).toHaveCount(0);

	// Asking is not doing: the button opens a confirmation, and Cancel and
	// Escape both leave the expression where it was.
	await panel.getByRole('button', { name: 'Use per-field inputs' }).click();
	const confirmation = panel.getByRole('alertdialog');
	await expect(confirmation).toBeVisible();
	await confirmation.getByRole('button', { name: 'Cancel' }).click();
	await expect(confirmation).toHaveCount(0);
	await expect(panel.getByText('These inputs are written as one expression')).toBeVisible();
	await panel.getByRole('button', { name: 'Use per-field inputs' }).click();
	await expect(confirmation).toBeVisible();
	await confirmation.focus();
	await page.keyboard.press('Escape');
	await expect(confirmation).toHaveCount(0);

	await panel.getByRole('button', { name: 'Use per-field inputs' }).click();
	await confirmation.getByRole('button', { name: 'Delete the expression' }).click();
	await expect(panel.getByRole('switch', { name: 'tempC: defined by the model' })).toHaveAttribute('aria-checked', 'true');
	await expect(panel.getByText('These inputs are written as one expression')).toHaveCount(0);

	const save = page.getByRole('button', { name: 'Save', exact: true });
	await expect(save).toBeEnabled();
	await save.click();
	await expect(save).toBeDisabled();
	const stored = await storedInputs(server.baseURL, parentId);
	expect(stored.mappingMode).toBe('defineBelow');
	expect(stored.value).toEqual({});
});

test('the static inputs a plain-JSON tool kept stay visible and removable when the workflow declares no fields', async ({ page, server }) => {
	const { parentId } = await seedToolWith(server.baseURL, [], { unit: 'F', note: { mode: 'expression', value: '{{ $json.note }}' } });
	const panel = await openToolPanel(page, server.baseURL, parentId);

	await expect(panel.getByText('Also sent, though the workflow does not declare them:')).toBeVisible();
	await expect(panel.getByRole('button', { name: 'Remove unit' })).toBeVisible();
	await expect(panel.getByRole('button', { name: 'Remove note' })).toBeVisible();

	await panel.getByRole('button', { name: 'Remove unit' }).click();
	await expect(panel.getByRole('button', { name: 'Remove unit' })).toHaveCount(0);
	const save = page.getByRole('button', { name: 'Save', exact: true });
	await expect(save).toBeEnabled();
	await save.click();
	await expect(save).toBeDisabled();
	const stored = await storedInputs(server.baseURL, parentId);
	expect(Object.keys(stored.value)).toEqual(['note']);
	expect(stored.value.note).toEqual({ mode: 'expression', value: '{{ $json.note }}' });
});
