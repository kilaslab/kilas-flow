import type { WorkflowPublishEventResource, WorkflowVersionSummaryResource } from '$lib/api/generated/models';
import * as m from '$lib/paraglide/messages.js';
import type { ConnectionChange, SettingChange } from './history-diff';

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
	if (summary.draft) roles.push({ label: m.versions_role_draft(), tone: 'bg-primary/15 text-primary border-primary/30' });
	if (summary.published) roles.push({ label: m.versions_role_published(), tone: 'bg-success/15 text-success border-success/30' });
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
	return label ? label : m.versions_revision_title({ revision: summary.revision });
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
	if (!value) return m.versions_time_unknown();
	const then = new Date(value);
	if (Number.isNaN(then.getTime())) return m.versions_time_unknown();

	const now = options.now ?? new Date();
	const seconds = Math.round((then.getTime() - now.getTime()) / 1000);
	const magnitude = Math.abs(seconds);
	// A server clock a few seconds ahead of the browser's would otherwise render
	// the revision the user just saved as arriving "in 4 seconds", which reads
	// as a bug in the editor rather than as a clock difference nobody can see.
	if (magnitude < 45) return m.versions_time_just_now();

	const format = new Intl.RelativeTimeFormat(options.locale, { numeric: 'auto' });
	for (const { unit, per, limit } of units) {
		if (magnitude < limit) return format.format(Math.round(seconds / per), unit);
	}
	return m.versions_time_just_now();
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
	if (!options.canRestore) return m.versions_refusal_cannot_change();
	if (options.isDraft) return m.versions_refusal_already_draft();
	if (options.dirty) return m.versions_refusal_unsaved();
	return null;
}

/** Why a revision cannot be published, or null when it can. */
export function publishRefusal(options: { canPublish: boolean; isPublished: boolean }): string | null {
	if (!options.canPublish) return m.versions_refusal_cannot_publish();
	if (options.isPublished) return m.versions_refusal_already_published();
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
	return m.versions_restore_confirmation({ title: versionTitle(summary) });
}

/** The sentence a publish is confirmed with, naming what starts serving traffic. */
export function publishConfirmation(summary: Pick<WorkflowVersionSummaryResource, 'revision' | 'label'>): string {
	return m.versions_publish_confirmation({ title: versionTitle(summary) });
}

/** One line of the publish timeline, describing what happened to a revision. */
export function publishEventSentence(event: Pick<WorkflowPublishEventResource, 'action'>, revisionLabel: string): string {
	switch (event.action) {
		case 'published':
			return m.versions_event_published({ revision: revisionLabel });
		case 'unpublished':
			return m.versions_event_unpublished({ revision: revisionLabel });
		case 'restored':
			return m.versions_event_restored({ revision: revisionLabel });
		default:
			return m.versions_event_changed({ revision: revisionLabel });
	}
}

/**
 * What to call the action a publish event records, for the badge beside its
 * sentence — the event's own word rather than the sentence it is stated in.
 */
export function publishEventActionLabel(event: Pick<WorkflowPublishEventResource, 'action'>): string {
	switch (event.action) {
		case 'published':
			return m.versions_action_published();
		case 'unpublished':
			return m.versions_action_unpublished();
		case 'restored':
			return m.versions_action_restored();
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
	return summary ? versionTitle(summary) : m.versions_pruned_revision();
}

/**
 * A connection difference as a sentence: the direction it was added or removed
 * in, named by the nodes it joins rather than by two identifiers.
 */
export function connectionChangeSentence(change: ConnectionChange): string {
	return change.status === 'added'
		? m.versions_connection_added({ source: change.sourceName, target: change.targetName })
		: m.versions_connection_removed({ source: change.sourceName, target: change.targetName });
}

/**
 * A settings difference as a sentence: the key read with what happened to it.
 *
 * The status is a word the reader has to understand rather than a token, which
 * is why it is spelled out per arm instead of being interpolated raw.
 */
export function settingChangeSentence(change: SettingChange): string {
	switch (change.status) {
		case 'added':
			return m.versions_setting_added({ key: change.key });
		case 'removed':
			return m.versions_setting_removed({ key: change.key });
		case 'changed':
			return m.versions_setting_changed({ key: change.key });
	}
}
