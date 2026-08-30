// Package embed issues and validates the short-lived sessions that authorise
// the iframe editor.
//
// Third-party cookies are unreliable, so the host backend mints a session and
// hands the token to the iframe over postMessage. Tokens carry the workflow,
// tenant, permissions and allowed origin.
//
// Not to be confused with internal/web, which embeds the SPA into the binary.
//
// Milestone 6.
package embed
