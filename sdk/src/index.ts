/**
 * KilasFlow host integration SDK.
 *
 * Two entry points, deliberately separate:
 *
 * - `@kilasflow/sdk/server` carries the host's API credentials and belongs on
 *   a backend. It manages workflows and mints embed sessions.
 * - `@kilasflow/sdk/browser` needs no credential at all. It mounts the
 *   embedded editor with a session the backend minted, and subscribes to
 *   execution events.
 *
 * The root export re-exports both for convenience in a full-stack framework;
 * a browser-only bundle should import `/browser` so no server code is
 * reachable from it.
 */

export * from './server.js';
export * from './browser.js';
export * from './version.js';
export type * from './generated/models.js';
