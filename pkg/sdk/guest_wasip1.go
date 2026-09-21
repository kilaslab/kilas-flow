//go:build wasip1

package sdk

import (
	"runtime"
	"unsafe"
)

// This file is the guest half of the pointer-and-length convention: it turns
// the byte slices the capability functions hold into the (pointer, length)
// pairs the ABI passes, and calls the generated imports directly.
//
// Calling the imports directly — rather than through a function value or an
// interface held in a variable — is what lets the Go linker drop the imports a
// pack never reaches, so a pack that declares no capability imports no host
// function at all. An indirection here would keep every import alive in every
// pack.

// pointerOf is the address of a slice's first byte as an i32, or 0 for an empty
// slice. The host treats a zero length as "no bytes" and never dereferences a
// zero pointer, so an empty argument is passed as (0, 0).
func pointerOf(b []byte) uint32 {
	if len(b) == 0 {
		return 0
	}
	return uint32(uintptr(unsafe.Pointer(unsafe.SliceData(b))))
}

func callHTTP(meta, body []byte) int32 {
	code := hostHTTPRequest(pointerOf(meta), uint32(len(meta)), pointerOf(body), uint32(len(body)))
	runtime.KeepAlive(meta)
	runtime.KeepAlive(body)
	return code
}

func callCredentialField(credentialType, field string) int32 {
	credentialTypeBytes, fieldBytes := []byte(credentialType), []byte(field)
	code := hostCredentialField(
		pointerOf(credentialTypeBytes), uint32(len(credentialTypeBytes)),
		pointerOf(fieldBytes), uint32(len(fieldBytes)))
	runtime.KeepAlive(credentialTypeBytes)
	runtime.KeepAlive(fieldBytes)
	return code
}

func callBinaryRead(id string) int32 {
	idBytes := []byte(id)
	code := hostBinaryRead(pointerOf(idBytes), uint32(len(idBytes)))
	runtime.KeepAlive(idBytes)
	return code
}

func callBinaryWrite(meta, data []byte) int32 {
	code := hostBinaryWrite(pointerOf(meta), uint32(len(meta)), pointerOf(data), uint32(len(data)))
	runtime.KeepAlive(meta)
	runtime.KeepAlive(data)
	return code
}

func callResultLen(slot int32) int32 {
	return hostResultLen(uint32(slot))
}

func callResultRead(slot, offset int32, dst []byte) int32 {
	code := hostResultRead(uint32(slot), uint32(offset), pointerOf(dst), uint32(len(dst)))
	runtime.KeepAlive(dst)
	return code
}
