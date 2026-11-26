package handlers

import (
	"context"

	"github.com/kilaslab/kilas-flow/internal/api/middleware"
	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// requestActor is the identity a request's write is attributed to: a
// signed-in person as a user, an API key as a key with its id, an embed
// session as embed, and nobody when authentication is off. It is the one
// definition of which authentication path becomes which actor kind, shared by
// the workflow history, the execution record and the credential audit trail so
// they cannot describe the same caller differently.
//
// An embed session is checked first because it is the narrower claim: the
// request carries a token minted for one workflow, whatever else surrounds it.
// Its label and ID are the actor the host named when it minted the session,
// signed into the token but never verified, so they attribute and grant
// nothing. A session minted without an actor still records embed, with no
// label: the trail says an embedded editor acted without inventing who.
func requestActor(ctx context.Context) repository.Actor {
	actor := repository.Actor{}
	if session, embedded := middleware.EmbedSessionFrom(ctx); embedded {
		actor.Kind = repository.ActorKindEmbed
		if session.Actor != nil {
			actor.Label = session.Actor.DisplayName
			actor.ID = session.Actor.ID
		}
		return actor
	}
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
