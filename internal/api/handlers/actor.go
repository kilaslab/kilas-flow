package handlers

import (
	"context"

	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// requestActor is the identity a request's write is attributed to: a
// signed-in person as a user, an API key as a key with its id, and nobody when
// authentication is off. It is the one definition of which authentication path
// becomes which actor kind, shared by the workflow history and the credential
// audit trail so the two cannot describe the same caller differently.
//
// An embed session never reaches a write that records an actor: it is refused
// every credential write, and its workflow writes carry no principal.
func requestActor(ctx context.Context) repository.Actor {
	actor := repository.Actor{}
	principal, found := auth.PrincipalFrom(ctx)
	if !found {
		return actor
	}
	actor.Label = principal.Label
	actor.KeyID = principal.KeyID
	switch principal.Kind {
	case auth.KindAPIKey:
		actor.Kind = repository.ActorKindKey
	case auth.KindSession:
		actor.Kind = repository.ActorKindUser
	}
	return actor
}
