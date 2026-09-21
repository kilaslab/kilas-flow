// Package wasmtest builds the smallest WebAssembly modules a test can run.
//
// The modules here are assembled by hand rather than produced by a Go build,
// for one reason: the runcode tests that matter are the ones about caching,
// limits and diagnostics, and every one of them used to need `go` on the
// machine to have anything to run. A build takes seconds and a toolchain;
// these modules take microseconds and none.
//
// Nothing here is a general WebAssembly assembler. It encodes exactly the
// sections the family of modules below needs, and a test that wants a module
// this package cannot express should build the bytes it wants rather than
// growing this file into a compiler.
//
// The byte-level layout every module here shares is documented once, on Build.
// The helpers beside it (I32Const, Call, Drop, Loop, Br, If, Unreachable) emit
// the instructions a host-call test needs, and ExpectResult composes them into
// the one assertion those tests make over and over: that the host answered
// exactly what the guest expected.
package wasmtest

import "encoding/binary"

// Section identifiers from the WebAssembly core specification.
const (
	sectionType     byte = 1
	sectionImport   byte = 2
	sectionFunction byte = 3
	sectionMemory   byte = 5
	sectionExport   byte = 7
	sectionCode     byte = 10
	sectionData     byte = 11
)

// ValueType is a WebAssembly value type, named by its encoding byte.
type ValueType byte

// The value types the assembler encodes.
const (
	I32 ValueType = 0x7f
	I64 ValueType = 0x7e
	F32 ValueType = 0x7d
	F64 ValueType = 0x7c
)

// Import is one function a module imports, with the signature the module
// calls it through.
//
// A module's imports are numbered before its own functions, so the index a
// Call takes for an import is its position in the slice passed to Build — the
// index, not the name, is what the instruction carries.
type Import struct {
	Module  string
	Name    string
	Params  []ValueType
	Results []ValueType
}

// DataSegment is one active data segment: bytes written into linear memory at
// Offset before _start runs.
type DataSegment struct {
	Offset uint32
	Bytes  []byte
}

// Build assembles a WASI command module: imports are its imported functions,
// startBody is the instruction sequence of its _start, memPages is the initial
// linear memory it declares in 64KiB pages, and data are its active data
// segments.
//
// The layout is fixed by the binary format, and every module this package
// builds is exactly this, in this order:
//
//	magic "\0asm" + version 1
//	type section:     one functype per distinct import signature, in the order
//	                  the imports are given, then () -> () for _start
//	import section:   one func import per Import, typed by its signature
//	function section: one function, _start, of type () -> ()
//	memory section:   one memory, min memPages, no maximum — so the host's own
//	                  limit is what bounds the module rather than a ceiling the
//	                  module author chose
//	export section:   "memory" (memory 0) and "_start" (func index
//	                  len(imports), which is the first function the module
//	                  defines)
//	code section:     _start: no locals, then startBody, then end
//	data section:     one active segment per DataSegment, at offset 0
//
// startBody is instructions only: Build writes the empty local declaration in
// front of it and the end after it. memPages is the module's declared initial
// memory and each data segment lands at its own offset; nothing here grows
// memory, and there is no table, global or element section, because no test
// has needed one.
func Build(imports []Import, startBody []byte, memPages uint32, data []DataSegment) []byte {
	// Signatures are deduplicated: a module that imports the same shape twice
	// (or imports () -> (), as a host function that only reports success does)
	// declares one functype and points both at it.
	var signatures []signature
	typeIndex := map[string]uint32{}
	typeOf := func(params, results []ValueType) uint32 {
		key := signatureKey(params, results)
		if index, found := typeIndex[key]; found {
			return index
		}
		typeIndex[key] = uint32(len(signatures))
		signatures = append(signatures, signature{params: params, results: results})
		return typeIndex[key]
	}

	importTypes := make([]uint32, len(imports))
	for index, imported := range imports {
		importTypes[index] = typeOf(imported.Params, imported.Results)
	}
	startType := typeOf(nil, nil)

	module := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00} // \0asm, version 1

	types := uleb128(uint32(len(signatures)))
	for _, declared := range signatures {
		types = append(types, 0x60) // functype
		types = append(types, uleb128(uint32(len(declared.params)))...)
		for _, param := range declared.params {
			types = append(types, byte(param))
		}
		types = append(types, uleb128(uint32(len(declared.results)))...)
		for _, result := range declared.results {
			types = append(types, byte(result))
		}
	}
	module = append(module, section(sectionType, types)...)

	if len(imports) > 0 {
		body := uleb128(uint32(len(imports)))
		for index, imported := range imports {
			body = append(body, wasmName(imported.Module)...)
			body = append(body, wasmName(imported.Name)...)
			body = append(body, 0x00) // func
			body = append(body, uleb128(importTypes[index])...)
		}
		module = append(module, section(sectionImport, body)...)
	}

	functions := append(uleb128(1), uleb128(startType)...) // one function: _start
	module = append(module, section(sectionFunction, functions)...)

	memories := append(uleb128(1), 0x00) // one memory, no maximum
	memories = append(memories, uleb128(memPages)...)
	module = append(module, section(sectionMemory, memories)...)

	exports := uleb128(2)
	exports = append(exports, wasmName("memory")...)
	exports = append(exports, 0x02) // memory
	exports = append(exports, uleb128(0)...)
	exports = append(exports, wasmName("_start")...)
	exports = append(exports, 0x00) // func
	exports = append(exports, uleb128(uint32(len(imports)))...)
	module = append(module, section(sectionExport, exports)...)

	function := make([]byte, 0, len(startBody)+2)
	function = append(function, 0x00) // no local declarations
	function = append(function, startBody...)
	function = append(function, 0x0b) // end
	code := uleb128(1)
	code = append(code, uleb128(uint32(len(function)))...)
	code = append(code, function...)
	module = append(module, section(sectionCode, code)...)

	if len(data) > 0 {
		segments := uleb128(uint32(len(data)))
		for _, segment := range data {
			segments = append(segments, 0x00, 0x41) // active, i32.const offset
			segments = append(segments, sleb128(int(segment.Offset))...)
			segments = append(segments, 0x0b) // end
			segments = append(segments, uleb128(uint32(len(segment.Bytes)))...)
			segments = append(segments, segment.Bytes...)
		}
		module = append(module, section(sectionData, segments)...)
	}

	return module
}

// MinimalModule returns a WASI command module whose _start writes stdout to
// file descriptor 1 and returns, ignoring its own input.
//
// It imports exactly one function, wasi_snapshot_preview1.fd_write, and
// exports only the memory and _start a command module needs. Its usefulness is
// that it is small, deterministic and toolchain-free: a test can execute it to
// prove something about the sandbox around it without a Go build anywhere in
// the loop.
//
// stdout is limited to what fits in the module's linear memory, which the
// module sizes from the payload — one page (64 KiB) for anything shorter than
// a page.
func MinimalModule(stdout string) []byte {
	const (
		iovecAddress   = 0 // the {pointer, length} pair, both little-endian uint32
		payloadAddress = 8 // where the pair points, immediately after itself
	)

	// The word fd_write reports the byte count in, placed after the payload so
	// a long string cannot overwrite it.
	nwrittenAddress := align8(payloadAddress + len(stdout))

	start := I32Const(1) // fd
	start = append(start, I32Const(iovecAddress)...)
	start = append(start, I32Const(1)...) // one iovec
	start = append(start, I32Const(nwrittenAddress)...)
	start = append(start, Call(0)...) // fd_write
	start = append(start, Drop()...)

	payload := make([]byte, payloadAddress+len(stdout))
	binary.LittleEndian.PutUint32(payload[0:4], payloadAddress)
	binary.LittleEndian.PutUint32(payload[4:8], uint32(len(stdout)))
	copy(payload[payloadAddress:], stdout)

	// One 64 KiB page holds the payload in every ordinary case; a longer
	// string simply gets the pages it needs.
	pages := uint32(1)
	for uint64(pages)*65536 < uint64(nwrittenAddress)+8 {
		pages++
	}

	return Build(
		[]Import{{
			Module:  "wasi_snapshot_preview1",
			Name:    "fd_write",
			Params:  []ValueType{I32, I32, I32, I32},
			Results: []ValueType{I32},
		}},
		start,
		pages,
		[]DataSegment{{Offset: 0, Bytes: payload}},
	)
}

// The opcodes the instruction helpers emit.
const (
	opUnreachable byte = 0x00
	opBlock       byte = 0x02
	opLoop        byte = 0x03
	opIf          byte = 0x04
	opBr          byte = 0x0c
	opCall        byte = 0x10
	opDrop        byte = 0x1a
	opI32Const    byte = 0x41
	opI32Ne       byte = 0x47
	blockTypeVoid byte = 0x40
	opEnd         byte = 0x0b
)

// I32Const pushes a 32-bit constant.
func I32Const(value int) []byte { return append([]byte{opI32Const}, sleb128(value)...) }

// Call calls the function at index, counting imports before the module's own
// functions.
func Call(index uint32) []byte { return append([]byte{opCall}, uleb128(index)...) }

// Drop discards the value on the top of the stack.
func Drop() []byte { return []byte{opDrop} }

// Loop wraps body in a block that a br 0 repeats, so `Loop(Br(0))` is an
// infinite loop — the guest a limit test needs.
func Loop(body []byte) []byte { return wrap(opLoop, body) }

// If wraps body in a block that runs it when the top of the stack is non-zero.
func If(body []byte) []byte { return wrap(opIf, body) }

// Br branches out to the block at depth, where 0 is the innermost.
func Br(depth uint32) []byte { return append([]byte{opBr}, uleb128(depth)...) }

// Unreachable traps.
func Unreachable() []byte { return []byte{opUnreachable} }

// ExpectResult checks the answer of the host function at index: it calls it,
// compares what came back with want, and traps unless they are equal.
//
//	call index; i32.const want; i32.ne; if; unreachable; end
//
// The alternative — returning the host's answer to the test, which then wants
// it in a structured form — needs a guest that encodes an i32 into a payload,
// and every test that asks this question is asking whether the host answered
// exactly what it should. A trap is the shortest honest way to say it did not.
func ExpectResult(index uint32, want int) []byte {
	body := Call(index)
	body = append(body, I32Const(want)...)
	body = append(body, opI32Ne)
	return append(body, If(Unreachable())...)
}

// wrap renders one block instruction with a void block type, its body and its
// end.
func wrap(opcode byte, body []byte) []byte {
	out := make([]byte, 0, len(body)+4)
	out = append(out, opcode, blockTypeVoid)
	out = append(out, body...)
	return append(out, opEnd)
}

// signature is one functype Build has to declare, deduplicated by its key.
type signature struct {
	params  []ValueType
	results []ValueType
}

// signatureKey identifies one functype, so Build can declare it once.
func signatureKey(params, results []ValueType) string {
	key := make([]byte, 0, len(params)+len(results)+1)
	for _, param := range params {
		key = append(key, byte(param))
	}
	key = append(key, 0x00) // separates params from results
	for _, result := range results {
		key = append(key, byte(result))
	}
	return string(key)
}

// section renders one section: its identifier, a ULEB128 length and its body.
func section(id byte, body []byte) []byte {
	out := []byte{id}
	out = append(out, uleb128(uint32(len(body)))...)
	return append(out, body...)
}

// wasmName renders a length-prefixed name.
func wasmName(name string) []byte {
	return append(uleb128(uint32(len(name))), name...)
}

// uleb128 encodes an unsigned LEB128 integer.
func uleb128(value uint32) []byte {
	var out []byte
	for {
		payload := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			out = append(out, payload|0x80)
			continue
		}
		return append(out, payload)
	}
}

// sleb128 encodes a signed LEB128 integer the way `i32.const` wants it.
func sleb128(value int) []byte {
	var out []byte
	for {
		payload := byte(value & 0x7f)
		value >>= 7
		signBitSet := payload&0x40 != 0
		if (value == 0 && !signBitSet) || (value == -1 && signBitSet) {
			return append(out, payload)
		}
		out = append(out, payload|0x80)
	}
}

// align8 rounds an offset up to the next multiple of eight, so the scratch
// word fd_write writes its byte count into never overlaps the payload.
func align8(offset int) int { return (offset + 7) &^ 7 }
