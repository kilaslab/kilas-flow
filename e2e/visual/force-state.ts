import type { CDPSession, Page } from '@playwright/test';

// Forces interaction pseudo-classes on the harness cells, so hover, pressed
// and focus-visible render without moving a pointer or pressing a key (only
// one element can be hovered or focused for real at a time). A cell names its
// states in `data-force` ("hover active", "focus focus-visible").
//
// Chromium only (DevTools protocol), which matches the projects. The forced
// state lives as long as the returned session: keep it until the screenshot.

const PSEUDO_CLASSES = ['hover', 'active', 'focus', 'focus-visible', 'focus-within'] as const;

export async function forceStates(page: Page): Promise<{ session: CDPSession; forced: number }> {
	const session = await page.context().newCDPSession(page);
	await session.send('DOM.enable');
	await session.send('CSS.enable');
	const { root } = await session.send('DOM.getDocument', { depth: -1 });
	const byNode = new Map<number, Set<string>>();
	for (const pseudo of PSEUDO_CLASSES) {
		const { nodeIds } = await session.send('DOM.querySelectorAll', {
			nodeId: root.nodeId,
			selector: `[data-force~="${pseudo}"]`
		});
		for (const nodeId of nodeIds) {
			const set = byNode.get(nodeId) ?? new Set<string>();
			set.add(pseudo);
			byNode.set(nodeId, set);
		}
	}
	for (const [nodeId, pseudos] of byNode) {
		await session.send('CSS.forcePseudoState', { nodeId, forcedPseudoClasses: [...pseudos] });
	}
	return { session, forced: byNode.size };
}
