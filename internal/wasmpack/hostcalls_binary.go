package wasmpack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/tetratelabs/wazero/api"

	"github.com/kilaslab/kilas-flow/internal/workflow"
	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

// binaryRead is the host side of sdk.ReadBinary.
//
// The reachable set is exactly the payloads the input items carry plus the ones
// this run wrote. That is deliberately narrower than "the tenant's payloads":
// an id is a capability, and a pack that was handed one item must not be able
// to enumerate the store by guessing ids.
func (b *binding) binaryRead(ctx context.Context, module api.Module, stack []uint64) int32 {
	if !b.calls.Enter(ctx, module) {
		return 0
	}
	b.run.clear()

	id, code := readGuestText(module, api.DecodeU32(stack[0]), api.DecodeU32(stack[1]), maxName)
	if code != 0 {
		return b.fail(code, "the payload id could not be read")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return b.fail(sdk.ErrInvalid, "binary_read needs a payload id")
	}
	if !b.run.canRead(id) {
		return b.fail(sdk.ErrDenied, "this run may only read payloads its input items carry or it wrote itself")
	}
	if b.inv.Request.Binaries == nil {
		return b.fail(sdk.ErrDenied, "this runtime has no payload store, so a pack cannot read payloads in it")
	}
	reader, ref, err := b.inv.Request.Binaries.Get(id)
	if err != nil {
		return b.fail(sdk.ErrNotFound, "the payload could not be read: "+err.Error())
	}
	defer reader.Close()

	contents, err := io.ReadAll(io.LimitReader(reader, maxBinary+1))
	if err != nil {
		return b.fail(sdk.ErrFailed, "the payload could not be read")
	}
	if int64(len(contents)) > maxBinary {
		return b.fail(sdk.ErrTooLarge, fmt.Sprintf("the payload is larger than the %d-byte cap a pack may read", int64(maxBinary)))
	}
	head, err := json.Marshal(sdk.BinaryRef{
		ID: ref.ID, FileName: ref.FileName, MediaType: ref.MediaType, Size: ref.Size,
	})
	if err != nil {
		return b.fail(sdk.ErrFailed, "the payload could not be described")
	}
	return b.succeed(head, contents)
}

// binaryWrite is the host side of sdk.WriteBinary.
//
// The payload goes into the runtime's own store, scoped to this execution's
// tenant by the store itself, exactly as a built-in node's output would. What
// the run wrote is recorded, so the executor can refuse an output item that
// carries a reference this run never produced.
func (b *binding) binaryWrite(ctx context.Context, module api.Module, stack []uint64) int32 {
	if !b.calls.Enter(ctx, module) {
		return 0
	}
	b.run.clear()

	metadata, code := readGuest(module, api.DecodeU32(stack[0]), api.DecodeU32(stack[1]), maxMetadata)
	if code != 0 {
		return b.fail(code, "the payload description could not be read")
	}
	contents, code := readGuest(module, api.DecodeU32(stack[2]), api.DecodeU32(stack[3]), maxBinary)
	if code != 0 {
		return b.fail(code, fmt.Sprintf("the payload could not be read, or is larger than the %d-byte cap a pack may write", int64(maxBinary)))
	}

	var description sdk.BinaryWrite
	if err := decodeStrict(metadata, &description); err != nil {
		return b.fail(sdk.ErrInvalid, "the payload description is not valid: "+err.Error())
	}
	name := strings.TrimSpace(description.Name)
	if name == "" {
		return b.fail(sdk.ErrInvalid, "binary_write needs a payload name")
	}
	if len(name) > maxName {
		return b.fail(sdk.ErrTooLarge, fmt.Sprintf("the payload name is longer than %d bytes", maxName))
	}
	if b.inv.Request.Binaries == nil {
		return b.fail(sdk.ErrDenied, "this runtime has no payload store, so a pack cannot store payloads in it")
	}
	ref, err := b.inv.Request.Binaries.Put(name, description.MediaType, bytes.NewReader(contents))
	if err != nil {
		return b.fail(sdk.ErrFailed, "the payload could not be stored: "+err.Error())
	}
	b.run.recordWrite(workflow.BinaryRef{
		ID: ref.ID, FileName: ref.FileName, MediaType: ref.MediaType, Size: ref.Size,
	})
	head, err := json.Marshal(sdk.BinaryRef{
		ID: ref.ID, FileName: ref.FileName, MediaType: ref.MediaType, Size: ref.Size,
	})
	if err != nil {
		return b.fail(sdk.ErrFailed, "the payload reference could not be described")
	}
	return b.succeed(head, nil)
}
