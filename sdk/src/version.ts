/**
 * The SDK's own version.
 *
 * It is separate from the API version on purpose: the SDK follows semver for
 * its surface, while the API is versioned by its `/api/v1` path. A host can
 * upgrade one without the other.
 */
export const SDK_VERSION = '0.1.0';

/** The API version this SDK targets. */
export const API_VERSION = 'v1';
