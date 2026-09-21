// FEAT-5fhj6p stage 2: the capstone's configuration, read once and purely.
//
// The capstone is the on-demand, real-credential run of the epic proofs
// against a docker IMAGE, the real Telegram Bot API, a real WAHA server and the
// npm registry. Everything about it that needs no credentials is provable
// against a locally built image (`make docker`) — the image host, the container
// no-Node check, proofs 3 and 4, corpus fidelity and the report — and the two
// credential-gated proofs skip with a reason and a recovery command instead of
// pretending.
//
// This module is pure: it reads an env object and returns a value, so the
// machinery tests and the report can exercise the defaulting and the missing
// reasons without a docker daemon, and no test mutates process.env to do so.
//
// The variable list is the documentation contract: the stage 3 docs-coverage
// guard test fails when the suite reads a KILASFLOW_CAPSTONE_ variable the
// operator page does not name.

/** The five outcomes vocabulary of the whole capstone: see recorded(). */
export const PROOF_IDS = [
	'00-artefacts',
	'01-proof1-telegram',
	'02-proof2-waha',
	'03-proof3-datastore',
	'04-proof3-isolation',
	'05-proof4-external',
	'06-corpus'
] as const;

export type ProofId = (typeof PROOF_IDS)[number];

export interface CapstoneMissing {
	reason: string;
	recovery: string;
}

export interface CapstoneConfig {
	image: string;
	/** true unless KILASFLOW_CAPSTONE_PULL=0: a local image is not pulled. */
	pull: boolean;
	/** 'tarball' packs ./sdk (the rehearsal); anything else is an npm spec. */
	sdkSpec: string;
	npmRegistry: string | null;
	pgImage: string;
	/** Keep the run's containers alive after a failure, for inspection. */
	keep: boolean;
	runId: string;
	corpusDir: string | null;
	corpusStrict: boolean;
	telegram: {
		botToken: string | null;
		chatId: string | null;
		openRouterKey: string | null;
		model: string | null;
	};
	waha: {
		url: string | null;
		apiKey: string | null;
		sessionA: string | null;
		sessionB: string | null;
	};
	/** null when the proof can run; otherwise why not, and how to fix it. */
	missing(proofId: string): CapstoneMissing | null;
}

function trimmed(value: string | undefined): string | null {
	if (typeof value !== 'string') return null;
	const text = value.trim();
	return text === '' ? null : text;
}

function runId(env: Record<string, string | undefined>): string {
	const explicit = trimmed(env.KILASFLOW_CAPSTONE_RUN_ID);
	if (explicit) return explicit;
	return `r${Math.random().toString(16).slice(2, 10)}`;
}

// The exact commands an operator runs to supply what a proof is missing. The
// gates.ts convention: what is missing, then the command, so the skip message
// is actionable rather than a diagnosis with no next step.
const REHEARSAL = 'KILASFLOW_CAPSTONE_IMAGE=kilasflow:latest KILASFLOW_CAPSTONE_PULL=0 KILASFLOW_CAPSTONE_SDK_SPEC=tarball make test-e2e-capstone e2e-capstone-report';

/**
 * Reads the capstone configuration. Pure: the only state it touches is the
 * object handed in, which defaults to process.env.
 */
export function readCapstoneConfig(env: Record<string, string | undefined> = process.env): CapstoneConfig {
	// The image under test is named in exactly one place, and the default is the
	// published coordinate. It deliberately does NOT fall back to the Makefile's
	// KILASFLOW_IMAGE/KILASFLOW_VERSION: those carry `git describe --always`, and
	// scripts/docker-tags.sh publishes only vX.Y.Z, vX.Y and latest — a
	// commit-derived tag is an image that can never be pulled, so a run that
	// inherited it would report "artefact missing" for a reason nobody could act
	// on. A rehearsal names its local tag explicitly (see REHEARSAL above); a
	// release run names the version tag it published.
	const image = trimmed(env.KILASFLOW_CAPSTONE_IMAGE) ?? 'ghcr.io/kilaslab/kilasflow:latest';
	const pull = trimmed(env.KILASFLOW_CAPSTONE_PULL) !== '0';
	const sdkSpec = trimmed(env.KILASFLOW_CAPSTONE_SDK_SPEC) ?? '@kilasflow/sdk@latest';
	const config: CapstoneConfig = {
		image,
		pull,
		sdkSpec,
		npmRegistry: trimmed(env.KILASFLOW_CAPSTONE_NPM_REGISTRY),
		pgImage: trimmed(env.KILASFLOW_CAPSTONE_PG_IMAGE) ?? 'pgvector/pgvector:pg17',
		keep: trimmed(env.KILASFLOW_CAPSTONE_KEEP) === '1' || trimmed(env.KILASFLOW_CAPSTONE_KEEP) === 'true',
		runId: runId(env),
		corpusDir: trimmed(env.KILASFLOW_CORPUS_DIR),
		corpusStrict: trimmed(env.KILASFLOW_CAPSTONE_CORPUS_STRICT) === '1',
		telegram: {
			botToken: trimmed(env.KILASFLOW_CAPSTONE_TELEGRAM_BOT_TOKEN),
			chatId: trimmed(env.KILASFLOW_CAPSTONE_TELEGRAM_CHAT_ID),
			openRouterKey: trimmed(env.KILASFLOW_CAPSTONE_OPENROUTER_API_KEY),
			model: trimmed(env.KILASFLOW_CAPSTONE_MODEL)
		},
		waha: {
			url: trimmed(env.KILASFLOW_CAPSTONE_WAHA_URL),
			apiKey: trimmed(env.KILASFLOW_CAPSTONE_WAHA_API_KEY),
			sessionA: trimmed(env.KILASFLOW_CAPSTONE_WAHA_SESSION_A),
			sessionB: trimmed(env.KILASFLOW_CAPSTONE_WAHA_SESSION_B)
		},
		missing: () => null
	};

	config.missing = (proofId: string): CapstoneMissing | null => {
		if (proofId === '01-proof1-telegram') {
			const required: Array<[string, string | null]> = [
				['KILASFLOW_CAPSTONE_TELEGRAM_BOT_TOKEN', config.telegram.botToken],
				['KILASFLOW_CAPSTONE_TELEGRAM_CHAT_ID', config.telegram.chatId],
				['KILASFLOW_CAPSTONE_OPENROUTER_API_KEY', config.telegram.openRouterKey],
				['KILASFLOW_CAPSTONE_MODEL', config.telegram.model]
			];
			const absent = required.filter(([, value]) => value === null).map(([name]) => name);
			if (absent.length > 0) {
				return {
					reason: `real Telegram proof needs ${absent.join(', ')} (bot token from BotFather, a chat that has started the bot, an OpenRouter key and its model id)`,
					recovery: `set ${absent.join('=')} and a public HTTPS tunnel host, then run \`${REHEARSAL}\``
				};
			}
			return null;
		}
		if (proofId === '02-proof2-waha') {
			const required: Array<[string, string | null]> = [
				['KILASFLOW_CAPSTONE_WAHA_URL', config.waha.url],
				['KILASFLOW_CAPSTONE_WAHA_API_KEY', config.waha.apiKey],
				['KILASFLOW_CAPSTONE_WAHA_SESSION_A', config.waha.sessionA],
				['KILASFLOW_CAPSTONE_WAHA_SESSION_B', config.waha.sessionB]
			];
			const absent = required.filter(([, value]) => value === null).map(([name]) => name);
			if (absent.length > 0) {
				return {
					reason: `real WAHA proof needs ${absent.join(', ')} (an operator-hosted WAHA server and two paired sessions)`,
					recovery: `set ${absent.join('=')} and run \`${REHEARSAL}\``
				};
			}
			return null;
		}
		return null;
	};

	return config;
}

/** The secrets a record or a report may never carry, in one place. */
export function capstoneSecrets(config: CapstoneConfig): string[] {
	return [
		config.telegram.botToken,
		config.telegram.openRouterKey,
		config.waha.apiKey,
		config.waha.url,
		config.npmRegistry
	].filter((value): value is string => typeof value === 'string' && value.length > 0);
}
