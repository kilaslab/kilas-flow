import { render } from 'svelte/server';
import { describe, expect, it } from 'vitest';

import WorkflowTitle from './workflow-title.svelte';

/** SSR needs no DOM, as in `webhook-address.test.ts`. */
function html(editable: boolean): string {
	const rendered = render(WorkflowTitle, { props: { name: 'Nightly sync', editable, onRename: () => null } });
	return rendered.body.replace(/<!--.*?-->/g, '');
}

describe('the workflow title', () => {
	it('is a button that starts a rename when the canvas can be written', () => {
		const markup = html(true);

		expect(markup).toContain('<button');
		expect(markup).toContain('Rename workflow');
		expect(markup).toContain('Nightly sync');
	});

	it('is plain text, with nothing to start a rename, on a canvas that cannot be written', () => {
		const markup = html(false);

		expect(markup).not.toContain('<button');
		expect(markup).not.toContain('<input');
		expect(markup).toContain('>Nightly sync<');
	});
});
