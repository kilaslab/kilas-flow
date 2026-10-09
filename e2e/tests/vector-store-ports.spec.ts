import type { Locator, Page } from '@playwright/test';

import { test, expect } from '../fixtures';
import { createWorkflow } from '../helpers/seed';

// A vector store and an embeddings node draw the ports their mode resolves
// (BUG-pc284d). The server resolves them through node.Definition.PortsFor; the
// canvas used to draw every one of these nodes with the static main in / main
// out pair, so the AI wires of an imported RAG workflow had no handle to land on.

const RETRIEVAL_DOCUMENT = {
	schemaVersion: 1,
	name: 'Retrieval Ports',
	nodes: [
		{ id: 'manual', name: 'Manual Trigger', type: 'kilasflow.manual', typeVersion: 1, position: { x: 0, y: 0 } },
		{ id: 'agent', name: 'Agent', type: 'kilasflow.agent', typeVersion: 1, position: { x: 400, y: 0 }, parameters: { prompt: 'Ask the handbook.' } },
		{ id: 'model', name: 'Agent Model', type: 'kilasflow.chatModel', typeVersion: 1, position: { x: 300, y: 200 }, parameters: { model: 'scripted' } },
		{ id: 'tool', name: 'Handbook Tool', type: 'kilasflow.vectorStoreTool', typeVersion: 1, position: { x: 500, y: 200 }, parameters: { toolName: 'handbook', toolDescription: 'The employee handbook.' } },
		{ id: 'tool-model', name: 'Tool Model', type: 'kilasflow.chatModel', typeVersion: 1, position: { x: 700, y: 400 }, parameters: { model: 'scripted' } },
		{ id: 'store', name: 'Policies Store', type: 'kilasflow.vectorStorePGVector', typeVersion: 1, position: { x: 400, y: 400 }, parameters: { mode: 'retrieve', tableName: 'documents' }, credentials: { postgres: 'pg-credential' } },
		{ id: 'embeddings', name: 'Embeddings', type: 'kilasflow.embeddings', typeVersion: 1, position: { x: 400, y: 600 }, parameters: { mode: 'cluster', model: 'text-embedding-3-small' } }
	],
	connections: [
		{ id: 'c-main', kind: 'main', source: { nodeId: 'manual', port: 'main' }, target: { nodeId: 'agent', port: 'main' } },
		{ id: 'c-agent-model', kind: 'ai_languageModel', source: { nodeId: 'model', port: 'model' }, target: { nodeId: 'agent', port: 'model' } },
		{ id: 'c-tool', kind: 'ai_tool', source: { nodeId: 'tool', port: 'tool' }, target: { nodeId: 'agent', port: 'tools' } },
		{ id: 'c-tool-model', kind: 'ai_languageModel', source: { nodeId: 'tool-model', port: 'model' }, target: { nodeId: 'tool', port: 'model' } },
		{ id: 'c-store', kind: 'ai_vectorStore', source: { nodeId: 'store', port: 'vectorStore' }, target: { nodeId: 'tool', port: 'vectorStore' } },
		{ id: 'c-embeddings', kind: 'ai_embedding', source: { nodeId: 'embeddings', port: 'embedding' }, target: { nodeId: 'store', port: 'embedding' } }
	],
	settings: {}
};

/** The handles one node draws, by direction and port name. */
async function handles(node: Locator): Promise<{ inputs: string[]; outputs: string[] }> {
	const read = async (selector: string) =>
		(await node.locator(selector).evaluateAll((elements) => elements.map((element) => element.getAttribute('data-handleid') ?? ''))).sort();
	return { inputs: await read('.svelte-flow__handle.target'), outputs: await read('.svelte-flow__handle.source') };
}

function nodeNamed(page: Page, id: string): Locator {
	return page.locator(`[data-testid="workflow-canvas"] .svelte-flow__node[data-id="${id}"]`);
}

function edge(page: Page, id: string): Locator {
	return page.locator(`[data-testid="workflow-canvas"] .svelte-flow__edge[data-id="${id}"]`);
}

test('a vector store and an embeddings node draw the ports their mode resolves, and choosing another mode redraws them', async ({ page, server }) => {
	const workflow = await createWorkflow(server.baseURL, RETRIEVAL_DOCUMENT);
	await page.goto(`${server.baseURL}/app/workflows/${workflow.id}`);
	await expect(page.locator('[data-testid="workflow-canvas"]')).toBeVisible();

	const store = nodeNamed(page, 'store');
	await expect(store).toBeVisible();

	// Retrieve mode: embeddings in, the store itself out, no items either way.
	await expect.poll(() => handles(store)).toEqual({ inputs: ['embedding'], outputs: ['vectorStore'] });
	// A cluster embeddings node has no input and one output.
	await expect.poll(() => handles(nodeNamed(page, 'embeddings'))).toEqual({ inputs: [], outputs: ['embedding'] });
	// The imported AI wires attach to those handles.
	await expect(edge(page, 'c-store')).toHaveCount(1);
	await expect(edge(page, 'c-embeddings')).toHaveCount(1);

	// Choosing another mode in the inspector redraws the handles.
	await store.click();
	const panel = page.getByRole('region', { name: 'Policies Store properties' });
	await expect(panel).toBeVisible();
	await panel.getByRole('tab', { name: 'Parameters' }).click();
	const mode = panel.getByRole('combobox', { name: 'Mode', exact: true });

	await mode.selectOption('retrieve-as-tool');
	await expect.poll(() => handles(store)).toEqual({ inputs: ['embedding'], outputs: ['tool'] });
	// The embeddings wire still fits and stays drawn; the vectorStore wire has
	// no handle any more, so the canvas cannot draw it.
	await expect(edge(page, 'c-embeddings')).toHaveCount(1);
	await expect(edge(page, 'c-store')).toHaveCount(0);

	// The wire the mode stranded is removed once the edit has settled, and the
	// status line says which one went (BUG-qfp7rf): an undrawn wire could be
	// neither selected nor deleted.
	const notice = page.getByRole('status').filter({ hasText: 'Removed 1 connection' });
	await expect(notice).toContainText('Policies Store has no port for any more');
	await expect(notice).toContainText('Policies Store vectorStore → Handbook Tool vectorStore');

	// One Undo takes the mode and the wire back together.
	await page.getByRole('button', { name: 'Undo', exact: true }).click();
	await expect(mode).toHaveValue('retrieve');
	await expect.poll(() => handles(store)).toEqual({ inputs: ['embedding'], outputs: ['vectorStore'] });
	await expect(edge(page, 'c-store')).toHaveCount(1);
	await expect(notice).toHaveCount(0);

	// Changing the mode again removes it again, and a save keeps it removed.
	await mode.selectOption('insert');
	await expect.poll(() => handles(store)).toEqual({ inputs: ['document', 'embedding'], outputs: ['main'] });
	await expect(notice).toContainText('Policies Store vectorStore → Handbook Tool vectorStore');

	const save = page.getByRole('button', { name: 'Save', exact: true });
	await save.click();
	await expect(save).toBeDisabled();
	const stored = await (await fetch(`${server.baseURL}/api/v1/workflows/${workflow.id}`)).json();
	const storedIDs = stored.latestVersion.document.connections.map((connection: { id: string }) => connection.id);
	expect(storedIDs).not.toContain('c-store');
	expect(storedIDs).toContain('c-embeddings');
});
