// The colour pairs the Kunyit token set must keep legible, checked in both
// themes by tokens.contrast.test.ts.
//
// This list was rebuilt from the wording of FEAT-p75n7j's second acceptance
// criterion because the audit's contrast-report-v2.txt (92 pairs) is not
// available in this repository. It holds 44 pairs per theme (88 in all). If
// the report is recovered, reconcile against it by ADDING pairs, never by
// dropping one: the count test below the list fails on a removal.

export type PairKind = 'text' | 'non-text' | 'state';

export interface ContrastPair {
	fg: string;
	bg: string;
	/** Opaque backdrop a translucent `bg` is composited over (default --kf-raised). */
	over?: string;
	min: number;
	kind: PairKind;
}

const TEXT = 4.5;
const NON_TEXT = 3;

const text = (fg: string, bg: string, over?: string): ContrastPair => ({ fg, bg, over, min: TEXT, kind: 'text' });
const line = (fg: string, bg: string): ContrastPair => ({ fg, bg, min: NON_TEXT, kind: 'non-text' });

const READING_SURFACES = ['--kf-bg', '--kf-raised', '--kf-overlay'];
const ALL_SURFACES = ['--kf-canvas', '--kf-bg', '--kf-panel', '--kf-raised', '--kf-overlay'];
const STATUSES = ['success', 'warning', 'danger', 'info'];
const CATEGORIES = ['trigger', 'ai', 'flow', 'data', 'app', 'utility'];

export const TOKEN_PAIRS: ContrastPair[] = [
	// text ≥ 4.5:1
	...['--kf-fg', '--kf-fg-muted', '--kf-fg-subtle'].flatMap((fg) => READING_SURFACES.map((bg) => text(fg, bg))),
	...['--kf-accent', '--kf-accent-hover', '--kf-accent-press'].map((bg) => text('--kf-accent-fg', bg)),
	text('--kf-accent-text', '--kf-bg'),
	...STATUSES.map((s) => text(`--kf-${s}`, '--kf-raised')),
	...STATUSES.map((s) => text(`--kf-${s}`, `--kf-${s}-subtle`, '--kf-raised')),
	...STATUSES.map((s) => text(`--kf-${s}-fill-fg`, `--kf-${s}-fill`)),
	// non-text ≥ 3:1
	...ALL_SURFACES.map((bg) => line('--kf-focus', bg)),
	...ALL_SURFACES.map((bg) => line('--kf-selection', bg)),
	...['--kf-edge', '--kf-edge-hover', '--kf-edge-attachment'].map((fg) => line(fg, '--kf-canvas')),
	...CATEGORIES.map((cat) => line(`--kf-cat-${cat}`, '--kf-raised'))
];

/** Guards the shadcn/Tailwind aliases that existing markup still uses. */
export const ALIAS_PAIRS: ContrastPair[] = [
	text('--foreground', '--background'),
	text('--foreground', '--card'),
	text('--foreground', '--popover'),
	text('--muted-foreground', '--background'),
	text('--muted-foreground', '--card'),
	text('--muted-foreground', '--muted'),
	text('--primary-foreground', '--primary'),
	text('--destructive', '--background'),
	text('--destructive', '--card'),
	text('--destructive-foreground', '--destructive'),
	text('--success', '--background'),
	text('--warning', '--background'),
	text('--warning-foreground', '--warning'),
	text('--sidebar-foreground', '--sidebar'),
	text('--accent-foreground', '--accent')
	// `text-primary` (--primary as text) is deliberately absent: the kunyit fill
	// is 1.7:1 on white in light. Text that should read in the accent moves to
	// --kf-accent-text under FEAT-r8ph93.
];

/**
 * The hover and pressed surfaces must read as a change on every surface a
 * hoverable row or control sits on, and as two different steps. A light
 * --kf-hover of .955 on the .962 panel was 1.02:1, so the sidebar's hover and
 * current-page fill vanished. 1.08:1 is the floor; it is a surface step, not
 * a text or boundary contrast.
 */
const STATE_SURFACES = ['--kf-bg', '--kf-panel', '--kf-raised', '--kf-overlay'];
const state = (fg: string, bg: string, min = 1.08): ContrastPair => ({ fg, bg, min, kind: 'state' });
export const STATE_PAIRS: ContrastPair[] = [
	...STATE_SURFACES.flatMap((bg) => [state('--kf-hover', bg), state('--kf-pressed', bg)]),
	state('--kf-pressed', '--kf-hover', 1.05)
];

/** Host accents an embed may set; the derived ink/white text must stay ≥ 4.5:1 on each. */
export const HOST_ACCENTS = ['#0ea5e9', '#1d4ed8', '#16a34a', '#dc2626', '#0891b2', '#111827'];
