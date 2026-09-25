package engine

import (
	"context"
	"errors"
	"sync"

	"github.com/kilaslab/kilas-flow/internal/credentials"
)

// resolvedSecrets collects the secret values of every credential a node
// resolved during one invocation, so the error it fails with can be scrubbed
// of them before the runner records it.
//
// The scrub happens at the one boundary every node error crosses — the
// invocation, right after the executor returns — rather than in each place an
// error is formatted, because an error becomes a trace row, the execution's
// own error, a Warn line and an error item handed to a "notify on failure"
// branch, and a secret stopped at only some of those still leaves. The
// values come from the resolver the node itself used, so they are exactly the
// secrets that node held and nothing is guessed from shape.
//
// Transport errors are already cut to their scheme and host at each outbound
// call site (safehttp.RedactError). This is the second line, for a secret an
// executor's own error text carried some other way.
type resolvedSecrets struct {
	mu     sync.Mutex
	values []string
}

func (secrets *resolvedSecrets) add(values []string) {
	secrets.mu.Lock()
	defer secrets.mu.Unlock()
	secrets.values = append(secrets.values, values...)
}

func (secrets *resolvedSecrets) snapshot() []string {
	secrets.mu.Lock()
	defer secrets.mu.Unlock()
	return append([]string(nil), secrets.values...)
}

// recording wraps a resolver so every credential it hands out is noted. A nil
// resolver stays nil, because a nil resolver is how a node learns credentials
// are not available in this runtime.
func (secrets *resolvedSecrets) recording(resolver CredentialResolver) CredentialResolver {
	if resolver == nil {
		return nil
	}
	return recordingResolver{inner: resolver, secrets: secrets}
}

// recordingResolver notes the secret values of each credential it resolves.
type recordingResolver struct {
	inner   CredentialResolver
	secrets *resolvedSecrets
}

func (resolver recordingResolver) ResolveCredential(ctx context.Context, credentialID string) (Credential, error) {
	credential, err := resolver.inner.ResolveCredential(ctx, credentialID)
	if err == nil {
		resolver.secrets.add(credentials.SecretValues(credential.Type, credential.Fields))
	}
	return credential, err
}

// scrub returns err with the collected secret values withheld from its text,
// or err itself when nothing was resolved.
func (secrets *resolvedSecrets) scrub(err error) error {
	if err == nil {
		return nil
	}
	values := secrets.snapshot()
	if len(values) == 0 {
		return err
	}
	return &scrubbedError{cause: err, secrets: values}
}

// scrubbedError is a node error with the secrets it resolved withheld.
//
// Unwrap still reaches the original, so errors.Is(err, context.DeadlineExceeded)
// and the runner's own classification answer as before. What the runner reads
// text out of through errors.As — the per-item outcomes of a whole-batch node,
// a batch failure, a message-alone error item — is handed back scrubbed by As,
// because errors.As consults As before it unwraps, and would otherwise find the
// original with the secret still in it.
type scrubbedError struct {
	cause   error
	secrets []string
}

func (err *scrubbedError) Error() string {
	return credentials.ScrubText(err.cause.Error(), err.secrets)
}

func (err *scrubbedError) Unwrap() error { return err.cause }

func (err *scrubbedError) As(target any) bool {
	switch typed := target.(type) {
	case *ItemOutcomes:
		var outcomes ItemOutcomes
		if !errors.As(err.cause, &outcomes) {
			return false
		}
		scrubbed := make(ItemOutcomes, len(outcomes))
		for index, outcome := range outcomes {
			scrubbed[index] = outcome
			if outcome.Err != nil {
				scrubbed[index].Err = &scrubbedError{cause: outcome.Err, secrets: err.secrets}
			}
		}
		*typed = scrubbed
		return true
	case **BatchFailure:
		var batch *BatchFailure
		if !errors.As(err.cause, &batch) {
			return false
		}
		*typed = &BatchFailure{Err: &scrubbedError{cause: batch.Err, secrets: err.secrets}}
		return true
	case *itemMessage:
		var described itemMessage
		if !errors.As(err.cause, &described) {
			return false
		}
		*typed = scrubbedMessage(credentials.ScrubText(described.ItemMessage(), err.secrets))
		return true
	}
	return false
}

// scrubbedMessage is a message-alone error item's text, already scrubbed.
type scrubbedMessage string

func (message scrubbedMessage) ItemMessage() string { return string(message) }
