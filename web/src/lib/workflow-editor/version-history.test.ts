import { afterEach, describe, expect, it } from 'vitest';

import type { WorkflowVersionSummaryResource } from '$lib/api/generated/models';
import { setLocale } from '$lib/i18n/locale.svelte';
import * as m from '$lib/paraglide/messages.js';
import type { ConnectionChange } from '$lib/workflow-editor/history-diff';
import {
	connectionChangeSentence,
	eventRevisionLabel,
	publishConfirmation,
	publishEventActionLabel,
	publishEventSentence,
	publishRefusal,
	relativeTime,
	restoreConfirmation,
	restoreRefusal,
	settingChangeSentence,
	versionAuthor,
	versionRoles,
	versionTitle
} from './version-history';

function summary(overrides: Partial<WorkflowVersionSummaryResource> = {}): WorkflowVersionSummaryResource {
	return {
		id: 'v-1',
		workflowId: 'wf-1',
		revision: 3,
		schemaVersion: 1,
		createdAt: '2026-09-05T10:00:00Z',
		draft: false,
		published: false,
		...overrides
	};
}

/** One connection difference, as `diffWorkflowDocuments` reports it. */
const connection: ConnectionChange = {
	connectionID: 'c-1',
	status: 'added',
	kind: 'main',
	sourceName: 'Webhook',
	targetName: 'Set',
	sourcePort: 'out',
	targetPort: 'in'
};

describe('versionRoles', () => {
	it('marks the newest revision as the draft', () => {
		expect(versionRoles(summary({ draft: true })).map((role) => role.label)).toEqual(['Draft']);
	});

	it('marks the revision production traffic runs as published', () => {
		expect(versionRoles(summary({ published: true })).map((role) => role.label)).toEqual(['Published']);
	});

	it('gives a revision that is both the draft and the published one both badges', () => {
		// Publishing the newest revision is the ordinary case, so the two roles
		// have to be able to coexist on one row.
		expect(versionRoles(summary({ draft: true, published: true })).map((role) => role.label)).toEqual(['Draft', 'Published']);
	});

	it('gives an ordinary historical revision no badge at all', () => {
		expect(versionRoles(summary())).toEqual([]);
	});
});

describe('versionTitle', () => {
	it('prefers the name a person gave the revision', () => {
		expect(versionTitle(summary({ label: 'Before the pricing change' }))).toBe('Before the pricing change');
	});

	it('falls back to the revision number when nobody named it', () => {
		expect(versionTitle(summary({ revision: 12 }))).toBe('Revision 12');
	});

	it('treats a label of only whitespace as no label', () => {
		expect(versionTitle(summary({ revision: 12, label: '   ' }))).toBe('Revision 12');
	});
});

describe('versionAuthor', () => {
	it('names the author when the deployment knew one', () => {
		expect(versionAuthor(summary({ createdBy: 'yusril' }))).toBe('yusril');
	});

	it('reports no author rather than inventing one', () => {
		// The main API has no authentication yet, so most revisions genuinely
		// have no author and the column is nullable for that reason.
		expect(versionAuthor(summary())).toBeNull();
		expect(versionAuthor(summary({ createdBy: '  ' }))).toBeNull();
	});
});

describe('relativeTime', () => {
	const now = new Date('2026-09-05T12:00:00Z');
	const locale = 'en-US';

	it('reads a revision saved seconds ago as just now', () => {
		expect(relativeTime('2026-09-05T11:59:40Z', { now, locale })).toBe('just now');
	});

	it('counts minutes, hours and days', () => {
		expect(relativeTime('2026-09-05T11:30:00Z', { now, locale })).toBe('30 minutes ago');
		expect(relativeTime('2026-09-05T09:00:00Z', { now, locale })).toBe('3 hours ago');
		expect(relativeTime('2026-09-02T12:00:00Z', { now, locale })).toBe('3 days ago');
	});

	it('reaches weeks, months and years for older revisions', () => {
		expect(relativeTime('2026-08-15T12:00:00Z', { now, locale })).toBe('3 weeks ago');
		expect(relativeTime('2026-06-05T12:00:00Z', { now, locale })).toBe('3 months ago');
		expect(relativeTime('2023-09-05T12:00:00Z', { now, locale })).toBe('3 years ago');
	});

	it('does not render a revision as arriving in the future when the server clock runs ahead', () => {
		// A few seconds of clock skew between the API and the browser must not
		// turn a revision the user just saved into "in 4 seconds".
		expect(relativeTime('2026-09-05T12:00:04Z', { now, locale })).toBe('just now');
	});

	it('says nothing rather than NaN for a missing or unparseable timestamp', () => {
		expect(relativeTime(undefined, { now, locale })).toBe('—');
		expect(relativeTime('', { now, locale })).toBe('—');
		expect(relativeTime('not a date', { now, locale })).toBe('—');
	});
});

describe('restoreRefusal', () => {
	it('lets a writer restore an older revision onto a clean canvas', () => {
		expect(restoreRefusal({ canRestore: true, dirty: false, isDraft: false })).toBeNull();
	});

	it('refuses while the canvas has unsaved changes', () => {
		// Both hosts remount the editor when a new revision becomes the latest,
		// and that remount discards the draft without asking.
		expect(restoreRefusal({ canRestore: true, dirty: true, isDraft: false })).toMatch(/unsaved changes/);
	});

	it('refuses to restore the revision that is already on the canvas', () => {
		expect(restoreRefusal({ canRestore: true, dirty: false, isDraft: true })).toMatch(/already the draft/);
	});

	it('refuses a session that cannot write', () => {
		expect(restoreRefusal({ canRestore: false, dirty: false, isDraft: false })).toMatch(/cannot change/);
	});

	it('names the missing permission before the unsaved changes', () => {
		// A read-only session cannot act on the advice to save first, so telling
		// it to save would send the user in a circle.
		expect(restoreRefusal({ canRestore: false, dirty: true, isDraft: false })).toMatch(/cannot change/);
	});
});

describe('publishRefusal', () => {
	it('lets a permitted session publish an older revision', () => {
		expect(publishRefusal({ canPublish: true, isPublished: false })).toBeNull();
	});

	it('refuses a session without the publish permission', () => {
		expect(publishRefusal({ canPublish: false, isPublished: false })).toMatch(/cannot publish/);
	});

	it('refuses to republish the revision already serving traffic', () => {
		expect(publishRefusal({ canPublish: true, isPublished: true })).toMatch(/already/);
	});
});

describe('restoreConfirmation', () => {
	it('states that restoring appends rather than deletes', () => {
		const sentence = restoreConfirmation(summary({ revision: 4 }));

		expect(sentence).toContain('Revision 4');
		expect(sentence).toMatch(/new revision/);
		expect(sentence).toMatch(/Nothing is deleted/);
	});

	it('confirms by the name a person gave the revision', () => {
		expect(restoreConfirmation(summary({ revision: 4, label: 'Known good' }))).toContain('Known good');
	});
});

describe('publishConfirmation', () => {
	it('states that publishing leaves the canvas alone', () => {
		const sentence = publishConfirmation(summary({ revision: 4 }));

		expect(sentence).toMatch(/production traffic/);
		expect(sentence).toMatch(/canvas is left alone/);
	});
});

describe('publishEventSentence', () => {
	it('distinguishes starting to serve, stopping, and being restored', () => {
		expect(publishEventSentence({ action: 'published' }, 'Revision 4')).toBe('Revision 4 started serving traffic');
		expect(publishEventSentence({ action: 'unpublished' }, 'Revision 4')).toBe('Revision 4 stopped serving traffic');
		expect(publishEventSentence({ action: 'restored' }, 'Revision 4')).toBe('Revision 4 was restored onto the canvas');
	});
});

describe('eventRevisionLabel', () => {
	it('names the revision an event points at', () => {
		expect(eventRevisionLabel('v-1', [summary({ id: 'v-1', revision: 7 })])).toBe('Revision 7');
	});

	it('still says something when the revision has been pruned away', () => {
		// The audit row deliberately carries no foreign key, so evidence outlives
		// the version it describes.
		expect(eventRevisionLabel('v-gone', [summary({ id: 'v-1' })])).toBe('A pruned revision');
	});
});

describe('the Indonesian catalog', () => {
	// Every helper above is a pure function over the catalogs, so switching the
	// locale is the only thing that proves their sentences are looked up rather
	// than baked in as English constants with a translation bolted on beside
	// them. The locale is put back afterwards because the module owns it for the
	// whole worker.
	afterEach(() => {
		setLocale('en');
	});

	it('renders the confirmations and the timeline from the id catalog', () => {
		expect(setLocale('id')).toBe(true);

		expect(restoreConfirmation(summary({ revision: 4 }))).toBe(
			'Revisi 4 akan disimpan sebagai revisi baru di atas riwayat. Tidak ada yang dihapus — setiap revisi, termasuk yang ada di kanvas sekarang, tetap ada di daftar.'
		);
		expect(publishConfirmation(summary({ revision: 4, label: 'Rilis stabil' }))).toContain('Rilis stabil akan menjadi versi yang dijalankan trafik produksi.');
		expect(publishEventSentence({ action: 'published' }, 'Revisi 4')).toBe('Revisi 4 mulai melayani trafik');
		expect(publishEventActionLabel({ action: 'unpublished' })).toBe('penerbitan dibatalkan');
	});

	it('pluralises the differences count and names the diff rows through the catalog arms, in both locales', () => {
		expect(m.versions_differences({ count: 1 })).toBe('1 difference');
		expect(m.versions_differences({ count: 3 })).toBe('3 differences');
		expect(m.versions_differences_cosmetic({ count: 3 })).toBe('3 differences, all of them cosmetic — only where nodes sit.');
		expect(connectionChangeSentence(connection)).toBe('added — Webhook → Set');
		expect(settingChangeSentence({ key: 'timeout', status: 'changed', before: 1, after: 2 })).toBe('timeout changed');

		expect(setLocale('id')).toBe(true);

		expect(m.versions_differences({ count: 1 })).toBe('1 perbedaan');
		expect(m.versions_differences_cosmetic({ count: 1 })).toBe('1 perbedaan, semuanya kosmetik — hanya soal posisi node.');
		expect(connectionChangeSentence(connection)).toBe('ditambahkan — Webhook → Set');
		expect(settingChangeSentence({ key: 'timeout', status: 'changed', before: 1, after: 2 })).toBe('timeout diubah');
	});

	it('reads the revision title, the badges and the relative time from the catalog too', () => {
		expect(setLocale('id')).toBe(true);

		expect(versionTitle(summary({ revision: 12 }))).toBe('Revisi 12');
		expect(versionRoles(summary({ draft: true, published: true })).map((role) => role.label)).toEqual(['Draf', 'Diterbitkan']);
		expect(relativeTime('2026-09-05T11:59:40Z', { now: new Date('2026-09-05T12:00:00Z'), locale: 'en-US' })).toBe('baru saja');
		expect(restoreRefusal({ canRestore: true, dirty: true, isDraft: false })).toMatch(/belum disimpan/);
	});
});
