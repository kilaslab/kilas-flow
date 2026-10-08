---
id: BUG-5dn8hr
title: Database TLS defaults do not verify the server certificate
status: done
priority: low
labels:
    - credentials
    - databases
    - tls
parent: EPIC-brpz48
created: "2026-09-25T12:48:09Z"
updated: "2026-10-08T16:10:00Z"
---

# Description

- Postgres defaults to `sslmode=require`, which does not verify the certificate (internal/sqlnode/sqlnode.go:613-621).
- MySQL TLS is optional and accepts `skip-verify` (internal/credentials/builtin.go:159).

# Acceptance Criteria
- [x] Decide and document the default (`verify-full`, or a UI warning).
- [x] A test pins the choice.

# Notes

## Fix (2026-10-08)

Decision: a database credential that says nothing about TLS gets a verified connection. Weakening it is an explicit choice on the credential.

- PostgreSQL: an empty `sslMode` is now `verify-full`, not `require`. `require`, `verify-ca`, `prefer`, `allow` and `disable` still work when the credential names them (`postgresDSN`, internal/sqlnode/sqlnode.go).
- MySQL/MariaDB: an empty `tls` is now `true`, so the driver verifies the certificate and host name. Before this it was no TLS at all, which sent the password in plaintext, a worse default than Postgres's. `false`, `skip-verify` and `preferred` still work when named (`mysqlDSN`).
- If verification fails, `Open` names the opt-out in the error: "the server's certificate could not be verified; set SSL mode to require (or TLS to skip-verify) to encrypt without verifying it". A self-signed server that used to connect hits this after the upgrade, and the message tells its owner which field to change instead of leaving them to switch TLS off.
- Form: `sslMode` has `Default: "verify-full"` and `tls` has `Default: "true"`, so a new credential starts with the same mode an empty field gets. The descriptions say what each mode checks. The old "a registered config name" was removed because this server registers none. The skills reference (CREDENTIAL_TYPES.md) states the empty-field meaning.
- Tests:
  - `TestADatabaseCredentialVerifiesTheServerCertificateByDefault` (internal/sqlnode/tls_test.go) runs a fake server per driver. It speaks the pre-TLS preamble (Postgres SSLRequest, MySQL protocol-41 greeting offering CLIENT_SSL) and presents an untrusted certificate for 127.0.0.1. With the default, the client must refuse the handshake with a `*tls.CertificateVerificationError`, and the error must name the opt-out. With `sslMode=require` / `tls=skip-verify`, the handshake must complete. RED before the fix: Postgres completed the handshake, and MySQL never attempted TLS.
  - `TestADatabaseCredentialFormDefaultsToAVerifiedConnection` (internal/credentials) pins the form defaults.
- Live-gated helpers (nodes/database_test.go, internal/loadoptions/sql_test.go, internal/sqlbuild/sqlbuild_test.go) now read `tls` off the DSN and default it to `false`, the same way they already treated `sslmode`. The DSNs in .pine/memory/live-databases.md work unchanged.
- Verified live against throwaway containers (postgres:16-alpine and mysql:8 on non-default ports, then removed). The Postgres-gated and MySQL-gated suites in sqlnode, loadoptions, sqlbuild and nodes pass. Against the real mysql:8, an empty `tls` is refused with the hint (`x509: "MySQL_Server_8.4.11_Auto_Generated_Server_Certificate" certificate is not standards compliant`), and `skip-verify`, `false` and `preferred` all connect.
- Not done: a field for a private CA certificate (for example the Amazon RDS CA bundle). Until one exists, a server signed by a CA outside the system trust store needs `require`/`skip-verify`.
