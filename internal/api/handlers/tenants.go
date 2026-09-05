package handlers

import (
	"context"

	"github.com/kilaslabs/kilas-flow/internal/api/middleware"
	"github.com/kilaslabs/kilas-flow/internal/auth"
	"github.com/kilaslabs/kilas-flow/internal/repository"
)

// PrincipalTenants resolves a request's tenant from whatever authenticated it.
//
// This is the seam every repository call already went through; it simply had
// nothing to read before. Nothing downstream changes.
type PrincipalTenants struct {
	// fallback is the tenant a request with no principal resolves to. It is
	// repository.DefaultTenantID only on an installation with authentication
	// turned off, where every caller is by definition the operator. With
	// authentication on it is empty, so a request that somehow reached a
	// handler unauthenticated scopes to a tenant that owns nothing rather than
	// to the tenant that owns everything.
	fallback string
}

var _ TenantResolver = (*PrincipalTenants)(nil)

// NewPrincipalTenants builds the resolver.
func NewPrincipalTenants(fallbackTenantID string) *PrincipalTenants {
	return &PrincipalTenants{fallback: fallbackTenantID}
}

// Resolve returns the tenant this request may touch.
//
// An embed session wins over an API key. A request carrying both is inside an
// iframe the host opened, and the embed session is the narrower authority of
// the two — it names one workflow, where the key names a whole tenant. Reading
// the key instead would silently widen a request the host deliberately confined.
func (resolver *PrincipalTenants) Resolve(ctx context.Context) repository.TenantScope {
	if session, found := middleware.EmbedSessionFrom(ctx); found {
		return repository.TenantScope{ID: session.TenantID}
	}
	if principal, found := auth.PrincipalFrom(ctx); found {
		return repository.TenantScope{ID: principal.TenantID}
	}
	return repository.TenantScope{ID: resolver.fallback}
}
