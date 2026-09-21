package wasmpack

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tetratelabs/wazero/api"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/engine"
	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

// httpCapableCredential reports whether a credential type can authenticate an
// HTTP request.
//
// A type whose Authenticate is nil does not sign anything — a database
// credential is the example that matters — so naming it in a pack's request
// would resolve a secret the host then could not use. Both the load-time audit
// and the run-time call check this, from this one definition, which is what
// keeps the internal-database credential structurally out of a pack's reach
// rather than out of its reach by convention.
func httpCapableCredential(credentialType string) bool {
	credentialType_, found := credentials.Default().Get(credentialType)
	return found && credentialType_.Authenticate != nil
}

// credentialField is the host side of sdk.CredentialField.
//
// It answers with a non-secret field and nothing else. A secret field is a
// refusal, not a redaction: the pack cannot ask for it and get a placeholder it
// might send upstream, because the only correct answer to "give me the token"
// is that the host applies the token itself (sdk.HTTPRequest.Credential).
func (b *binding) credentialField(ctx context.Context, module api.Module, stack []uint64) int32 {
	if !b.calls.Enter(ctx, module) {
		return 0
	}
	b.run.clear()

	credentialType, code := readGuestText(module, api.DecodeU32(stack[0]), api.DecodeU32(stack[1]), maxName)
	if code != 0 {
		return b.fail(code, "the credential type could not be read")
	}
	field, code := readGuestText(module, api.DecodeU32(stack[2]), api.DecodeU32(stack[3]), maxName)
	if code != 0 {
		return b.fail(code, "the field name could not be read")
	}
	credentialType, field = strings.TrimSpace(credentialType), strings.TrimSpace(field)
	if credentialType == "" || field == "" {
		return b.fail(sdk.ErrInvalid, "credential_field needs both a credential type and a field name")
	}
	if !b.caps.GrantedCredential(credentialType) {
		return b.fail(sdk.ErrDenied, fmt.Sprintf("the pack did not declare the %s credential", credentialType))
	}

	credential, attached, err := b.attachedCredential(ctx, credentialType)
	if err != nil {
		return b.fail(sdk.ErrFailed, err.Error())
	}
	if !attached {
		return b.fail(sdk.ErrDenied, fmt.Sprintf("no %s credential is attached to this node", credentialType))
	}

	secret, public := credentials.Split(credential.Type, credential.Fields)
	if _, isSecret := secret[field]; isSecret {
		return b.fail(sdk.ErrDenied, fmt.Sprintf(
			"%s is a secret field of the %s credential; the host applies secrets to a request instead of handing them to a pack",
			field, credentialType))
	}
	value, found := public[field]
	if !found {
		return b.fail(sdk.ErrNotFound, fmt.Sprintf("the %s credential has no %s field", credentialType, field))
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return b.fail(sdk.ErrFailed, "the field could not be described")
	}
	return b.succeed(encoded, nil)
}

// attachedCredential resolves one credential type the node attached, once per
// run.
func (b *binding) attachedCredential(ctx context.Context, credentialType string) (engine.Credential, bool, error) {
	return b.run.cachedCredential(credentialType, func() (engine.Credential, bool, error) {
		return b.inv.Request.ResolveAttachedCredential(ctx, b.inv.IR, credentialType)
	})
}
