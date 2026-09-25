package jsrun

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// The byte encodings Node names, for Buffer and crypto: strings to bytes and
// back. They are Go's to do, over a whole buffer at once, rather than a
// character at a time in JavaScript.
func init() {
	registerNative("codec.decode", func(args []any) (any, error) {
		text, err := argString(args, 0, "the string")
		if err != nil {
			return nil, err
		}
		encoding, err := argString(args, 1, "the encoding")
		if err != nil {
			return nil, err
		}
		return decodeString(text, encoding)
	})
	registerNative("codec.validUTF8", func(args []any) (any, error) {
		data, err := argBytes(args, 0, "the bytes")
		if err != nil {
			return nil, err
		}
		return utf8.Valid(data), nil
	})
	registerNative("codec.encode", func(args []any) (any, error) {
		data, err := argBytes(args, 0, "the bytes")
		if err != nil {
			return nil, err
		}
		encoding, err := argString(args, 1, "the encoding")
		if err != nil {
			return nil, err
		}
		return encodeBytes(data, encoding)
	})
}

// normalEncoding is Node's name for an encoding, which it matches without
// regard to case, or "" for one Node does not know.
func normalEncoding(name string) string {
	switch strings.ToLower(name) {
	case "", "utf8", "utf-8":
		return "utf8"
	case "hex":
		return "hex"
	case "base64":
		return "base64"
	case "base64url":
		return "base64url"
	case "latin1", "binary":
		return "latin1"
	case "ascii":
		return "ascii"
	case "ucs2", "ucs-2", "utf16le", "utf-16le":
		return "utf16le"
	}
	return ""
}

func unknownEncoding(name string) error {
	return typeError("Unknown encoding: %s", name)
}

// decodeString turns a string into bytes as Node's Buffer.from(string,
// encoding) does. Like Node, it is lenient: hex stops at the first pair that
// is not hex, and base64 skips what is not base64.
func decodeString(text, encoding string) ([]byte, error) {
	switch normalEncoding(encoding) {
	case "utf8":
		return []byte(toWellFormed(text)), nil
	case "hex":
		out := make([]byte, 0, len(text)/2)
		for index := 0; index+1 < len(text); index += 2 {
			pair, err := hex.DecodeString(text[index : index+2])
			if err != nil {
				break
			}
			out = append(out, pair[0])
		}
		return out, nil
	case "base64", "base64url":
		return decodeBase64(text), nil
	case "latin1", "ascii":
		out := make([]byte, 0, len(text))
		for _, unit := range utf16.Encode([]rune(text)) {
			out = append(out, byte(unit))
		}
		return out, nil
	case "utf16le":
		units := utf16.Encode([]rune(text))
		out := make([]byte, 0, 2*len(units))
		for _, unit := range units {
			out = append(out, byte(unit), byte(unit>>8))
		}
		return out, nil
	}
	return nil, unknownEncoding(encoding)
}

// encodeBytes turns bytes into a string as Node's buf.toString(encoding) does.
func encodeBytes(data []byte, encoding string) (string, error) {
	switch normalEncoding(encoding) {
	case "utf8":
		return decodeUTF8WHATWG(data), nil
	case "hex":
		return hex.EncodeToString(data), nil
	case "base64":
		return base64.StdEncoding.EncodeToString(data), nil
	case "base64url":
		return base64.RawURLEncoding.EncodeToString(data), nil
	case "latin1":
		runes := make([]rune, len(data))
		for index, value := range data {
			runes[index] = rune(value)
		}
		return string(runes), nil
	case "ascii":
		runes := make([]rune, len(data))
		for index, value := range data {
			runes[index] = rune(value & 0x7f)
		}
		return string(runes), nil
	case "utf16le":
		units := make([]uint16, len(data)/2)
		for index := range units {
			units[index] = uint16(data[2*index]) | uint16(data[2*index+1])<<8
		}
		return string(utf16.Decode(units)), nil
	}
	return "", unknownEncoding(encoding)
}

// decodeBase64 reads standard or URL-safe base64, padded or not, skipping
// characters outside the alphabet, as Node does.
func decodeBase64(text string) []byte {
	var clean strings.Builder
	for _, char := range text {
		switch {
		case char >= 'A' && char <= 'Z', char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			clean.WriteRune(char)
		case char == '+' || char == '-':
			clean.WriteByte('+')
		case char == '/' || char == '_':
			clean.WriteByte('/')
		case char == '=':
			// Padding ends the data.
			goto done
		}
	}
done:
	data := clean.String()
	decoded, err := base64.RawStdEncoding.DecodeString(data[:len(data)-len(data)%4] + tail(data))
	if err != nil {
		return nil
	}
	return decoded
}

// tail keeps a final partial quantum base64 can still decode: two or three
// characters carry one or two bytes; a single one carries none.
func tail(data string) string {
	if rest := len(data) % 4; rest >= 2 {
		return data[len(data)-rest:]
	}
	return ""
}

// toWellFormed replaces lone surrogates, which a JavaScript string can hold
// and UTF-8 cannot, with U+FFFD, as Node does when it encodes one.
func toWellFormed(text string) string {
	if utf8.ValidString(text) {
		return text
	}
	return strings.ToValidUTF8(text, "�")
}

// decodeUTF8WHATWG turns bytes into a string the way Node's Buffer and
// TextDecoder do: the WHATWG Encoding Standard's UTF-8 decoder
// (https://encoding.spec.whatwg.org/#utf-8-decoder), written from the spec's
// own algorithm rather than Go's. Go's strings.ToValidUTF8 replaces each RUN
// of invalid bytes with one U+FFFD; WHATWG's "maximal subpart" rule instead
// replaces each byte that cannot start or continue a sequence with its own
// U+FFFD, and a truncated-but-otherwise-valid prefix with exactly one
// U+FFFD (BUG-46g75c).
//
// The state machine tracks, across bytes, how many continuation bytes the
// current sequence still needs and the [lower, upper] range the next one
// must fall in — which narrows for the byte right after E0, ED, F0 and F4,
// the four lead bytes whose second byte cannot span the whole 0x80-0xBF
// continuation range without admitting an overlong form, a surrogate, or a
// code point past U+10FFFF.
func decodeUTF8WHATWG(data []byte) string {
	var out strings.Builder
	out.Grow(len(data))

	var codePoint rune
	var bytesSeen, bytesNeeded int
	lower, upper := byte(0x80), byte(0xbf)

	for index := 0; index < len(data); {
		value := data[index]
		if bytesNeeded == 0 {
			switch {
			case value <= 0x7f:
				out.WriteByte(value)
			case value >= 0xc2 && value <= 0xdf:
				bytesNeeded, codePoint = 1, rune(value&0x1f)
			case value >= 0xe0 && value <= 0xef:
				if value == 0xe0 {
					lower = 0xa0
				} else if value == 0xed {
					upper = 0x9f
				}
				bytesNeeded, codePoint = 2, rune(value&0x0f)
			case value >= 0xf0 && value <= 0xf4:
				if value == 0xf0 {
					lower = 0x90
				} else if value == 0xf4 {
					upper = 0x8f
				}
				bytesNeeded, codePoint = 3, rune(value&0x07)
			default:
				out.WriteRune(utf8.RuneError) // a continuation byte, or 0xc0/0xc1/0xf5-0xff, with no lead of its own
			}
			index++
			continue
		}
		if value < lower || value > upper {
			// This byte cannot continue the sequence in progress: the
			// sequence so far becomes one U+FFFD, and the byte is
			// reprocessed as the possible start of its own sequence,
			// without advancing past it.
			codePoint, bytesSeen, bytesNeeded = 0, 0, 0
			lower, upper = 0x80, 0xbf
			out.WriteRune(utf8.RuneError)
			continue
		}
		lower, upper = 0x80, 0xbf // only the byte right after certain leads is narrowed
		codePoint = codePoint<<6 | rune(value&0x3f)
		bytesSeen++
		index++
		if bytesSeen != bytesNeeded {
			continue
		}
		out.WriteRune(codePoint)
		codePoint, bytesSeen, bytesNeeded = 0, 0, 0
	}
	if bytesNeeded != 0 {
		out.WriteRune(utf8.RuneError) // a valid prefix truncated at the end of the buffer
	}
	return out.String()
}
