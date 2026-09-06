/**
 * The SDK's own version, mirroring `version` in package.json.
 *
 * The two MUST agree: `make sdk-version-check` (run in CI) fails the build
 * when they differ, so a release can never publish one number while the code
 * reports another.
 *
 * It is separate from the API version on purpose: the SDK follows semver for
 * its surface — major on a breaking change, minor on additive surface, patch
 * on fixes — while the API is versioned by its `/api/v1` path. A host can
 * upgrade one without the other. Bumping the API version requires a new
 * served path prefix, never anything less. See
 * `docs/src/content/docs/reference/api-contract.md` for the full contract.
 */
export const SDK_VERSION = '0.1.0';

/** The API version this SDK targets. Tracks the `/api/v1` path prefix. */
export const API_VERSION = 'v1';
