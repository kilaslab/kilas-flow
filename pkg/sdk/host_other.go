//go:build !wasip1

package sdk

import (
	"encoding/json"
	"errors"
)

// This file is what the SDK looks like off the guest target.
//
// A pack is only ever built for wasip1, but `go build ./...`, `go vet ./...`
// and an author's own `go test ./...` run on the developer's machine, and the
// capability functions above have to link there. So the six host calls exist on
// this side too, with the same names and the same slot protocol, answering
// either from a fake host a test installed or with a refusal that says why
// there is nothing to call.

// Host is the native stand-in for the host module, for testing a pack's logic
// without a sandbox.
//
// It is deliberately the semantic shape of the ABI rather than its byte
// protocol: a test implements four methods, not six functions over linear
// memory. The slot protocol itself is exercised by the real host's tests.
type Host interface {
	HTTP(HTTPRequest, []byte) (HTTPResponse, []byte, *HostError)
	CredentialField(credentialType, field string) (string, *HostError)
	ReadBinary(id string) ([]byte, *HostError)
	WriteBinary(name, mediaType string, data []byte) (BinaryRef, *HostError)
}

// ErrNoHost reports a capability call in a build that has no host module and no
// fake host installed.
var ErrNoHost = errors.New("no host is available: this build is not a wasip1 guest and no fake host was set")

// fakeHost is the installed fake, if any. It is a package variable rather than
// a parameter because the capability functions' signatures are the ABI's, and
// the ABI has no room for a host argument.
var fakeHost Host

// SetHost installs a fake host for the current process. Pass nil to remove it.
//
// It exists so a pack's logic can be tested with `go test` on any machine:
// install a fake that answers what the test needs, call the pack's run
// function, assert on what it did. Nothing about the sandbox is proven that
// way, which is exactly why the host's own tests drive real modules.
func SetHost(host Host) { fakeHost = host }

// nativeSlots mirrors the host's two result slots.
var nativeSlots [2][]byte

func callHTTP(meta, body []byte) int32 {
	nativeSlots = [2][]byte{}
	if fakeHost == nil {
		return nativeFailure(ErrFailed, ErrNoHost.Error())
	}
	var request HTTPRequest
	if err := json.Unmarshal(meta, &request); err != nil {
		return nativeFailure(ErrInvalid, "request could not be decoded: "+err.Error())
	}
	response, responseBody, hostErr := fakeHost.HTTP(request, body)
	if hostErr != nil {
		return nativeHostError(hostErr)
	}
	head, err := json.Marshal(response)
	if err != nil {
		return nativeFailure(ErrInvalid, "response could not be encoded: "+err.Error())
	}
	nativeSlots[SlotResult] = head
	nativeSlots[SlotBody] = responseBody
	return int32(len(head))
}

func callCredentialField(credentialType, field string) int32 {
	nativeSlots = [2][]byte{}
	if fakeHost == nil {
		return nativeFailure(ErrFailed, ErrNoHost.Error())
	}
	value, hostErr := fakeHost.CredentialField(credentialType, field)
	if hostErr != nil {
		return nativeHostError(hostErr)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nativeFailure(ErrInvalid, "credential field could not be encoded: "+err.Error())
	}
	nativeSlots[SlotResult] = encoded
	return int32(len(encoded))
}

func callBinaryRead(id string) int32 {
	nativeSlots = [2][]byte{}
	if fakeHost == nil {
		return nativeFailure(ErrFailed, ErrNoHost.Error())
	}
	contents, hostErr := fakeHost.ReadBinary(id)
	if hostErr != nil {
		return nativeHostError(hostErr)
	}
	nativeSlots[SlotBody] = contents
	return 0
}

func callBinaryWrite(meta, data []byte) int32 {
	nativeSlots = [2][]byte{}
	if fakeHost == nil {
		return nativeFailure(ErrFailed, ErrNoHost.Error())
	}
	var description BinaryWrite
	if err := json.Unmarshal(meta, &description); err != nil {
		return nativeFailure(ErrInvalid, "payload could not be decoded: "+err.Error())
	}
	ref, hostErr := fakeHost.WriteBinary(description.Name, description.MediaType, data)
	if hostErr != nil {
		return nativeHostError(hostErr)
	}
	encoded, err := json.Marshal(ref)
	if err != nil {
		return nativeFailure(ErrInvalid, "payload reference could not be encoded: "+err.Error())
	}
	nativeSlots[SlotResult] = encoded
	return int32(len(encoded))
}

func callResultLen(slot int32) int32 {
	if slot < 0 || int(slot) >= len(nativeSlots) {
		return ErrInvalid
	}
	return int32(len(nativeSlots[slot]))
}

func callResultRead(slot, offset int32, dst []byte) int32 {
	if slot < 0 || int(slot) >= len(nativeSlots) {
		return ErrInvalid
	}
	contents := nativeSlots[slot]
	if offset < 0 || int(offset) > len(contents) {
		return ErrInvalid
	}
	copied := copy(dst, contents[offset:])
	return int32(copied)
}

// nativeFailure writes a refusal into the result slot and returns its code,
// mirroring what the host does.
func nativeFailure(code int32, message string) int32 {
	encoded, err := json.Marshal(HostError{Code: ErrorCodeName(code), Message: message})
	if err == nil {
		nativeSlots[SlotResult] = encoded
	}
	return code
}

// nativeHostError writes a fake host's refusal into the result slot.
func nativeHostError(hostErr *HostError) int32 {
	code := ErrFailed
	switch hostErr.Code {
	case CodeInvalid:
		code = ErrInvalid
	case CodeDenied:
		code = ErrDenied
	case CodeBlocked:
		code = ErrBlocked
	case CodeTooLarge:
		code = ErrTooLarge
	case CodeNotFound:
		code = ErrNotFound
	}
	encoded, err := json.Marshal(hostErr)
	if err == nil {
		nativeSlots[SlotResult] = encoded
	}
	return code
}
