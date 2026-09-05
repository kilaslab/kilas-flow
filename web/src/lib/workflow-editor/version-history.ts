import type { WorkflowPublishEventResource, WorkflowVersionSummaryResource } from '$lib/api/generated/models';

/**
 * Reading a workflow's version history.
 *
 * The rules a version panel enforces — when a restore may run, what a
 * confirmation has to say, which badges a revision carries — live here rather
 * than in the panel component for the reason `activation.ts` gives for its
 * own: two surfaces mount that panel, and a rule kept in markup grows a second
 * slightly different copy for each of them.
 */

/** A pill on a version row, as label plus the tone classes that colour it. */
export type VersionRole = { label: string; tone: string };

/**
 * Which roles a revision holds right now.
 *
 * `draft` and `published` are two independent booleans rather than one enum
 * because a revision is routinely both — publishing the newest revision is the
 * ordinary case, and an enum would have to drop one of the two answers. Both
 * are stated by the server, so nothing here infers publication by comparing
 * identifiers.
 */
export function versionRoles(summary: Pick<WorkflowVersionSummaryResource, 'draft' | 'published'>): VersionRole[] {
	const roles: VersionRole[] = [];
	if (summary.draft) roles.push({ label: 'Draft', tone: 'bg-primary/15 text-primary border-primary/30' });
	if (summary.published) roles.push({ label: 'Published', tone: 'bg-success/15 text-success border-success/30' });
	return roles;
}

/** Who saved a revision, or null when the deployment never knew. */
export function versionAuthor(summary: Pick<WorkflowVersionSummaryResource, 'createdBy'>): string | null {
	const author = summary.createdBy?.trim();
	return author ? author : null;
}

/** What to call a revision in a sentence, preferring the name a person gave it. */
export function versionTitle(summary: Pick<WorkflowVersionSummaryResource, 'revision' | 'label'>): string {
	const label = summary.label?.trim();
	return label ? label : `Revision ${summary.revision}`;
}

const units: { unit: Intl.RelativeTimeFormatUnit; per: number; limit: number }[] = [
	{ unit: 'minute', per: 60, limit: 3_600 },
	{ unit: 'hour', per: 3_600, limit: 86_400 },
	{ unit: 'day', per: 86_400, limit: 604_800 },
	{ unit: 'week', per: 604_800, limit: 2_629_800 },
	{ unit: 'month', per: 2_629_800, limit: 31_557_600 },
	{ unit: 'year', per: 31_557_600, limit: Number.POSITIVE_INFINITY }
];

/**
 * How long ago a revision was saved, in words.
 *
 * `now` and `locale` are parameters rather than ambient reads so this stays a
 * pure function a table of fixtures can pin down. Left to `new Date()` and the
 * host locale, the only assertion a test could make is the one that does not
 * catch anything.
 */
export function relativeTime(value: string | null | undefined, options: { now?: Date; locale?: string } = {}): string {
	if (!value) return '—';
	const then = new Date(value);
	if (Number.isNaN(then.getTime())) return '—';

	const now = options.now ?? new Date();
	const seconds = Math.round((then.getTime() - now.getTime()) / 1000);
	const magnitude = Math.abs(seconds);
	// A server clock a few seconds ahead of the browser's would otherwise render
	// the revision the user just saved as arriving "in 4 seconds", which reads
	// as a bug in the editor rather than as a clock difference nobody can see.
	if (magnitude < 45) return 'just now';

	const format = new Intl.RelativeTimeFormat(options.locale, { numeric: 'auto' });
	for (const { unit, per, limit } of units) {
		if (magnitude < limit) return format.format(Math.round(seconds / per), unit);
	}
	return 'just now';
}

/**
 * Why a restore cannot run, or null when it can.
 *
 * Restoring replaces what is on the canvas, so it is refused outright while
 * there are unsaved changes rather than being offered with a warning: both
 * hosts remount the editor when a new revision becomes the latest, and that
 * remount discards the draft without asking. Refusing here is what keeps a
 * user's unsaved work from disappearing behind a button they thought was safe.
 */
export function restoreRefusal(options: { canRestore: boolean; dirty: boolean; isDraft: boolean }): string | null {
	if (!options.canRestore) return 'This session cannot change this workflow.';
	if (options.isDraft) return 'This revision is already the draft on the canvas.';
	if (options.dirty) return 'Save or undo your unsaved changes first — restoring replaces what is on the canvas.';
	return null;
}

/** Why a revision cannot be published, or null when it can. */
export function publishRefusal(options: { canPublish: boolean; isPublished: boolean }): string | null {
	if (!options.canPublish) return 'This session cannot publish this workflow.';
	if (options.isPublished) return 'This revision is already the one serving traffic.';
	return null;
}

/**
 * The sentence a restore is confirmed with.
 *
 * It states that history is append-only, because the word "restore" reads as
 * "roll back and lose everything since" to most people, and the one thing a
 * user needs to know before pressing it is that nothing goes away.
 */
export function restoreConfirmation(summary: Pick<WorkflowVersionSummaryResource, 'revision' | 'label'>): string {
	return `${versionTitle(summary)} will be saved as a new revision on top of the history. Nothing is deleted — every revision, including the one on the canvas now, stays in the list.`;
}

/** The sentence a publish is confirmed with, naming what starts serving traffic. */
export function publishConfirmation(summary: Pick<WorkflowVersionSummaryResource, 'revision' | 'label'>): string {
	return `${versionTitle(summary)} will become the version production traffic runs. The revision on the canvas is left alone, and the change is recorded in the publish history.`;
}

/** One line of the publish timeline, describing what happened to a revision. */
export function publishEventSentence(event: Pick<WorkflowPublishEventResource, 'action'>, revisionLabel: string): string {
	switch (event.action) {
		case 'published':
			return `${revisionLabel} started serving traffic`;
		case 'unpublished':
			return `${revisionLabel} stopped serving traffic`;
		case 'restored':
			return `${revisionLabel} was restored onto the canvas`;
		default:
			return `${revisionLabel} changed`;
	}
}

/** Tone classes for a publish-timeline row, so an unpublish reads as a stop. */
export function publishEventTone(event: Pick<WorkflowPublishEventResource, 'action'>): string {
	switch (event.action) {
		case 'published':
			return 'bg-success/15 text-success border-success/30';
		case 'unpublished':
			return 'bg-warning/15 text-warning border-warning/30';
		default:
			return 'bg-muted text-muted-foreground border-border';
	}
}

/**
 * What to call the revision a publish event names.
 *
 * An event outlives the version it describes on purpose — the audit row has no
 * foreign key, so evidence survives a prune — which means the summary is
 * routinely missing and the panel has to say something anyway.
 */
export function eventRevisionLabel(versionID: string, summaries: WorkflowVersionSummaryResource[]): string {
	const summary = summaries.find((candidate) => candidate.id === versionID);
	return summary ? versionTitle(summary) : 'A pruned revision';
}
