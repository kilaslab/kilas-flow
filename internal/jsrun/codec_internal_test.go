package jsrun

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Each encoding round-trips, and matches what Node's Buffer produces for the
// same input.
func TestTheCodecMatchesNodesEncodings(t *testing.T) {
	cases := []struct {
		encoding, text string
		bytes          []byte
	}{
		{"utf8", "héllo 😀", []byte("héllo 😀")},
		{"UTF-8", "a", []byte("a")},
		{"hex", "00ff10", []byte{0x00, 0xff, 0x10}},
		{"base64", "aGk/Pz4+", []byte("hi??>>")},
		{"base64url", "aGk_Pz4-", []byte("hi??>>")},
		{"latin1", "éÿ", []byte{0xe9, 0xff}},
		{"binary", "A", []byte{0x41}},
		{"utf16le", "hé", []byte{0x68, 0x00, 0xe9, 0x00}},
	}
	for _, check := range cases {
		decoded, err := decodeString(check.text, check.encoding)
		if err != nil || !bytes.Equal(decoded, check.bytes) {
			t.Errorf("decode %q as %s = %v, %v; want %v", check.text, check.encoding, decoded, err, check.bytes)
		}
		encoded, err := encodeBytes(check.bytes, check.encoding)
		if err != nil || encoded != check.text {
			t.Errorf("encode %v as %s = %q, %v; want %q", check.bytes, check.encoding, encoded, err, check.text)
		}
	}
	// Node is lenient on input: hex stops at the first bad pair, base64 skips
	// what is not base64 and reads unpadded input, and ascii keeps seven bits.
	lenient := []struct {
		encoding, text string
		bytes          []byte
	}{
		{"hex", "0102zz03", []byte{1, 2}},
		{"base64", "aG k=\n", []byte("hi")},
		{"base64", "aGk", []byte("hi")},
	}
	for _, check := range lenient {
		if decoded, _ := decodeString(check.text, check.encoding); !bytes.Equal(decoded, check.bytes) {
			t.Errorf("decode %q as %s = %v, want %v", check.text, check.encoding, decoded, check.bytes)
		}
	}
	if text, _ := encodeBytes([]byte{0xc1}, "ascii"); text != "A" {
		t.Errorf("ascii keeps seven bits: got %q", text)
	}
	if _, err := decodeString("x", "klingon"); err == nil {
		t.Error("an unknown encoding was accepted")
	}
}

// utf8Case is one recorded byte sequence and the string Node 24's
// Buffer#toString('utf8') produced for it (testdata/parity/utf8.json,
// recorded by scripts/js-parity/record.mjs).
type utf8Case struct {
	Bytes []byte `json:"bytes"`
	Want  string `json:"want"`
}

// TestEncodeBytesUTF8MatchesTheRecordedNodeGoldens pins encodeBytes's "utf8"
// case (the codec.decode/encode native every Buffer#toString('utf8'),
// TextDecoder and getBinaryDataBuffer(...).toString() call reaches) against a
// byte-sequence sweep recorded from Node 24: lone continuation bytes, every
// lead byte truncated, a boundary sweep of the byte after each lead class,
// overlong forms, surrogate encodings, code points past U+10FFFF, and valid
// text around the malformed runs.
//
// Node's Buffer follows the WHATWG "maximal subpart" rule: each byte that
// cannot start or continue a sequence becomes its own U+FFFD, and a truncated
// but otherwise valid prefix becomes one U+FFFD. Go's strings.ToValidUTF8
// instead collapses a whole run of bad bytes into one U+FFFD, which is BUG-46g75c.
func TestEncodeBytesUTF8MatchesTheRecordedNodeGoldens(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "parity", "utf8.json"))
	if err != nil {
		t.Fatalf("reading the utf8.json golden: %v", err)
	}
	var golden struct {
		Cases []utf8Case `json:"cases"`
	}
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatalf("decoding the utf8.json golden: %v", err)
	}
	if len(golden.Cases) == 0 {
		t.Fatal("the utf8.json golden has no cases")
	}
	for _, check := range golden.Cases {
		got, err := encodeBytes(check.Bytes, "utf8")
		if err != nil {
			t.Fatalf("encodeBytes(%v, utf8) error = %v", check.Bytes, err)
		}
		if got != check.Want {
			t.Errorf("encodeBytes(%v, utf8) = %q, want %q (Node 24)", check.Bytes, got, check.Want)
		}
	}
}
