// The vocabulary every environment gate in these specs speaks.
//
// A skipped test is not a passing test, and the CI summary counts it as
// neither. The skip-budget check (e2e/scripts/skip-budget.mjs) is what makes
// them visible, and it can only do that by *reason*: a message assembled ad hoc
// in each spec — a probe error, a command a developer should run, the port a
// service was expected on — differs every time the environment differs. The
// prefix below is the part that stays the same, so `GATE.model` in a spec is
// what `local model unavailable` in e2e/skip-budget.json matches.
//
// Why the distinction matters: three of these four describe something CI cannot
// have (a local model, a corpus whose upstream carries no licence, credentials
// for a live n8n), and the fourth describes something CI *does* have. A
// PostgreSQL skip in CI therefore means the service, the DSN or the connection
// is broken, which is a failure rather than an accepted gap — see the
// `forbidden` list in e2e/skip-budget.json.
export const GATE = {
	postgres: 'postgres driver unavailable',
	model: 'local model unavailable',
	corpus: 'WAHA corpus unavailable',
	n8nLive: 'live n8n unavailable'
} as const;

export type Gate = keyof typeof GATE;

// gate builds a skip message: which dependency is missing, then the exact thing
// a developer runs to supply it. The second half is for the human reading the
// report; only the first is load-bearing.
export function gate(kind: Gate, detail: string): string {
	return `${GATE[kind]}: ${detail}`;
}
