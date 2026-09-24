package jsrun

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// A file returned inline is how n8n's older code made a file before
// prepareBinaryData existed: an item's binary entry holding the file's bytes
// as base64 text in data, beside its name and type. n8n accepts one as the
// file's contents, so the runtime does too.
//
// The worker never stores anything. The bytes come back in the result, where
// the output cap bounds them, and the process that prepared the job decodes
// them again and stores each one as prepareBinaryData stores a file, once the
// whole result is known to be good. Each counts as one host call, as the
// prepareBinaryData call it stands in for would.

// inlineFile is one file the code returned inline, decoded.
type inlineFile struct {
	data     []byte
	fileName string
	mimeType string
}

// readInline reads the file an entry with base64 text in data gives. Its
// name and type are optional, as prepareBinaryData's are; when they are there
// they must be text, and the type a media type.
func readInline(fields map[string]json.RawMessage, where, property string) (*inlineFile, error) {
	var text string
	if json.Unmarshal(fields["data"], &text) != nil {
		return nil, notAFile(where, property)
	}
	data, ok := decodeInlineBase64(text)
	if !ok {
		return nil, named(ErrInvalidReturn, fmt.Sprintf("%s has a binary %q whose data is not base64 text", where, property))
	}
	if size := int64(len(data)); size > MaxFileBytes {
		return nil, FileLimitError("a file", size)
	}
	file := &inlineFile{data: data}
	if raw, given := optional(fields, "fileName"); given && json.Unmarshal(raw, &file.fileName) != nil {
		return nil, named(ErrInvalidReturn, fmt.Sprintf("%s has a binary %q whose fileName is not text", where, property))
	}
	if raw, given := optional(fields, "mimeType"); given {
		if json.Unmarshal(raw, &file.mimeType) != nil {
			return nil, named(ErrInvalidReturn, fmt.Sprintf("%s has a binary %q whose mimeType is not text", where, property))
		}
		if strings.TrimSpace(file.mimeType) != "" {
			if _, _, err := mime.ParseMediaType(file.mimeType); err != nil {
				return nil, named(ErrInvalidReturn, fmt.Sprintf("%s has a binary %q whose mimeType %q is not a media type", where, property, file.mimeType))
			}
		}
	}
	return file, nil
}

// truthyJSON is JavaScript's truth table over a JSON value: null, false, 0
// and the empty string are false, and everything else true.
func truthyJSON(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case float64:
		return typed != 0
	case string:
		return typed != ""
	}
	return true
}

// optional is a field the code set to something other than null.
func optional(fields map[string]json.RawMessage, name string) (json.RawMessage, bool) {
	raw, present := fields[name]
	return raw, present && string(raw) != "null"
}

// decodeInlineBase64 reads base64 as it arrives from elsewhere: wrapped in
// lines, in either alphabet, with or without its padding. Anything else is
// refused. Node's decoder, which Buffer.from follows (decodeBase64), skips
// what is not base64 instead, and a file stored from those bytes would hold
// something other than what its author meant; a refusal says so.
func decodeInlineBase64(text string) ([]byte, bool) {
	clean := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n', '\f', '\v':
			return -1
		}
		return r
	}, text)
	clean = strings.TrimRight(clean, "=")
	encoding := base64.RawStdEncoding
	if strings.ContainsAny(clean, "-_") {
		encoding = base64.RawURLEncoding
	}
	data, err := encoding.DecodeString(clean)
	return data, err == nil
}

// notAFile is a returned binary entry that is neither a file reference nor a
// file given inline.
func notAFile(where, property string) error {
	return named(ErrInvalidReturn, fmt.Sprintf("%s has a binary %q that is neither a file reference nor a file given inline; "+
		"a binary entry can pass on a file the node was given or stored with prepareBinaryData, or give a file's bytes as base64 text in data", where, property))
}

// inlineHostCalls is the host-call limit reached by counting the files a
// result returned inline.
func inlineHostCalls(limit int, perItem bool) error {
	scope := ""
	if perItem {
		scope = " for one item"
	}
	return named(ErrHostCallLimit, fmt.Sprintf("code made more than %d host calls%s, counting each file returned inline as one", limit, scope))
}

// pendingFile is a file returned inline that Finish is yet to store, and
// where its reference goes: the returned item's binary, under property.
type pendingFile struct {
	file     *inlineFile
	refs     map[string]workflow.BinaryRef
	property string
	where    string
}

// storeInline stores the files a result returned inline, through the path
// prepareBinaryData's files take, and puts their references in the items.
func (job Job) storeInline(ctx context.Context, pending []pendingFile) error {
	for _, waiting := range pending {
		ref, err := job.ledger.writeFile(ctx, job.Roots.Helpers, waiting.file.data, waiting.file.fileName, waiting.file.mimeType)
		if err != nil {
			return fmt.Errorf("storing the file %s returned inline in binary %q: %w", waiting.where, waiting.property, err)
		}
		waiting.refs[waiting.property] = ref
	}
	return nil
}
