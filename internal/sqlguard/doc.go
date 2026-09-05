// Package sqlguard decides whether a piece of SQL text is one statement this
// server is willing to send.
//
// It exists because binding parameters is not a complete control. Every driver
// this server speaks will run two statements from one call when the text
// carries two — verified against each pinned driver, including through a
// prepared handle and including when parameters are bound — so a single
// interpolated semicolon turns a read into whatever the attacker wrote. The
// same call is reachable from an inbound webhook body, which is why the answer
// has to be enforcement rather than advice.
//
// # What it does, and what it deliberately does not
//
// It answers one question: is this text exactly one statement, and is that
// statement's opening keyword one the operation is allowed to use? It is not a
// SQL parser, it does not understand semantics, and it does not make arbitrary
// SQL safe. A SELECT the guard admits can still call a function the credential
// should not have been granted — PostgreSQL's pg_read_file, MySQL's LOAD_FILE
// — and the control for that is the privileges on the credential, not this
// package. The guard closes multi-statement injection and dangerous-verb
// injection. Saying more than that would be a claim the code does not support.
//
// # Why a lexer rather than a scan for dangerous words
//
// A denylist over the raw text loses to a comment: /*x*/ATTACH is an ATTACH
// that no substring search for "ATTACH" at the start will find, while a search
// anywhere in the text refuses a legitimate statement carrying the word inside
// a string literal. Neither addresses the multi-statement execution that made
// the read possible in the first place. So the text is lexed, split, and the
// opening keyword of the single surviving statement is matched against an
// allowlist.
//
// # Why erring toward refusal is sound here, where sniffing was rejected before
//
// sqlnode.Statement.Returning is declared rather than sniffed from the text,
// and its comment argues that looking for a leading SELECT is defeated by a
// comment or a CTE. That argument is right, and it does not apply here, because
// the two questions differ in which direction a wrong answer falls. Returning
// needs a *correct* answer: guessing wrong reports zero rows affected on a real
// write. This guard needs a *safe* answer: when the lexer and the server might
// disagree about where a string ends or a comment closes, it refuses. A
// refusal is a visible error the user can act on; a wrong allow is an
// exfiltrated credential table.
//
// That principle is why the lexer runs twice over every statement. The one
// place a lexer can be tricked into seeing *fewer* statements than the server
// is a backslash inside a string literal: whether \' ends the string depends on
// standard_conforming_strings on PostgreSQL and on sql_mode on MySQL, neither
// of which this process can see, and one of which an attacker can reach. So the
// text is lexed under both interpretations and both passes must agree that
// there is exactly one allowlisted statement. Guessing is replaced by requiring
// both answers to be safe.
package sqlguard
