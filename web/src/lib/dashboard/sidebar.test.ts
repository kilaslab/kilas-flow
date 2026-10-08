import { describe, expect, it } from 'vitest';

import { EDITOR_SIDEBAR_BREAKPOINT, isEditorRoute, sidebarCollapsed } from './sidebar';

describe('sidebarCollapsed', () => {
	it('follows the stored preference away from the editor', () => {
		expect(sidebarCollapsed({ stored: false, editorRoute: false, compact: true, editorChoice: null })).toBe(false);
		expect(sidebarCollapsed({ stored: true, editorRoute: false, compact: false, editorChoice: null })).toBe(true);
	});

	it('collapses on the editor below the breakpoint so the canvas keeps its width', () => {
		expect(sidebarCollapsed({ stored: false, editorRoute: true, compact: true, editorChoice: null })).toBe(true);
	});

	it('keeps the stored preference on a wide editor', () => {
		expect(sidebarCollapsed({ stored: false, editorRoute: true, compact: false, editorChoice: null })).toBe(false);
	});

	it('honours a sidebar the user opened on a narrow editor', () => {
		expect(sidebarCollapsed({ stored: false, editorRoute: true, compact: true, editorChoice: false })).toBe(false);
	});

	it('names a breakpoint near 1400 px', () => {
		expect(EDITOR_SIDEBAR_BREAKPOINT).toBe(1400);
	});
});

describe('isEditorRoute', () => {
	it('matches a workflow editor and nothing around it', () => {
		expect(isEditorRoute('/app/workflows/wf_123')).toBe(true);
		expect(isEditorRoute('/app/workflows')).toBe(false);
		expect(isEditorRoute('/app/workflows/wf_123/executions')).toBe(false);
	});
});
