package jsrun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// wireBinary is a file as a body sees it: its metadata, never its bytes. A
// body that reads `binary.data.data` finds nothing there and fails, instead
// of quietly decoding an empty payload.
type wireBinary struct {
	ID            string `json:"id"`
	FileName      string `json:"fileName,omitempty"`
	MimeType      string `json:"mimeType,omitempty"`
	FileExtension string `json:"fileExtension,omitempty"`
	FileSize      int64  `json:"fileSize"`
}

// wireInput is an input item as it crosses into the VM.
type wireInput struct {
	JSON   map[string]any        `json:"json"`
	Binary map[string]wireBinary `json:"binary,omitempty"`
}

// wireOutput is a returned item as it comes back: normalised by the runtime,
// with the input item it descends from in Paired, a number or "lost".
type wireOutput struct {
	JSON   map[string]any             `json:"json"`
	Binary map[string]json.RawMessage `json:"binary"`
	Paired json.RawMessage            `json:"paired"`
}

func encodeItems(items []workflow.Item) (string, error) {
	wire := make([]wireInput, len(items))
	for index, item := range items {
		wire[index].JSON = item.JSON
		if wire[index].JSON == nil {
			wire[index].JSON = map[string]any{}
		}
		if len(item.Binary) > 0 {
			wire[index].Binary = make(map[string]wireBinary, len(item.Binary))
			for property, ref := range item.Binary {
				wire[index].Binary[property] = wireBinary{
					ID: ref.ID, FileName: ref.FileName, MimeType: ref.MediaType,
					FileExtension: strings.TrimPrefix(path.Ext(ref.FileName), "."), FileSize: ref.Size,
				}
			}
		}
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(wire); err != nil {
		return "", fmt.Errorf("the node's input cannot be handed to code: %w", err)
	}
	return buffer.String(), nil
}

// decoder turns returned items back into workflow items, against the input
// they may descend from.
type decoder struct {
	// origins are the input items' own origins, by index.
	origins []*workflow.PairedItem
	// files are the input's files by ID. A returned item may pass one on; it
	// may not name a file it was never given.
	files map[string]workflow.BinaryRef
}

func (d *decoder) decode(text string, eachItem bool) ([]workflow.Item, error) {
	var wire []wireOutput
	if err := json.Unmarshal([]byte(text), &wire); err != nil {
		return nil, fmt.Errorf("jsrun: decoding the code's result: %w", err)
	}
	items := make([]workflow.Item, len(wire))
	for index, returned := range wire {
		where := fmt.Sprintf("item %d", index)
		if eachItem {
			where = "the returned value"
		}
		items[index].JSON = returned.JSON
		if items[index].JSON == nil {
			items[index].JSON = map[string]any{}
		}
		binary, err := d.binary(returned.Binary, where)
		if err != nil {
			return nil, err
		}
		items[index].Binary = binary
		items[index].Paired = d.paired(returned.Paired)
	}
	return items, nil
}

// binary checks each returned file against the input's files. Binary is
// passed on only when the code returns it, as n8n does; the Go Code node's
// positional carry-over does not apply here.
func (d *decoder) binary(returned map[string]json.RawMessage, where string) (map[string]workflow.BinaryRef, error) {
	if len(returned) == 0 {
		return nil, nil
	}
	refs := make(map[string]workflow.BinaryRef, len(returned))
	for property, raw := range returned {
		var file wireBinary
		if json.Unmarshal(raw, &file) != nil || file.ID == "" {
			return nil, named(ErrInvalidReturn, fmt.Sprintf("%s has a binary %q that is not a file reference; binary entries can only pass on files the node was given", where, property))
		}
		ref, known := d.files[file.ID]
		if !known {
			return nil, named(ErrInvalidReturn, fmt.Sprintf("%s has a binary %q naming a file this node was not given", where, property))
		}
		// The name and type are metadata the code may change; the bytes and
		// their size stay the file's own.
		if file.FileName != "" {
			ref.FileName = file.FileName
		}
		if file.MimeType != "" {
			ref.MediaType = file.MimeType
		}
		refs[property] = ref
	}
	return refs, nil
}

// paired maps the runtime's lineage onto the engine's. An item that descends
// from input item N inherits the origin that item carries, which is what the
// runner itself does for a node that passes items through one to one. An
// item with no known source is left for the runner to infer.
func (d *decoder) paired(raw json.RawMessage) *workflow.PairedItem {
	if len(raw) == 0 || string(raw) == "null" {
		// An item built from nothing descends from the node's only input item
		// when there was just one: n8n's own rule for an output without
		// pairedItem. Without it, a Code node that turns one item into several
		// leaves every one of them lost, and `$('Code').item` fails downstream.
		if len(d.origins) == 1 {
			return d.inherit(0)
		}
		return nil
	}
	if string(raw) == `"lost"` {
		return &workflow.PairedItem{Lost: true}
	}
	var index int
	if json.Unmarshal(raw, &index) != nil || index < 0 || index >= len(d.origins) {
		return nil
	}
	return d.inherit(index)
}

// inherit is the origin input item index carries, copied, or nil when it
// carries none.
func (d *decoder) inherit(index int) *workflow.PairedItem {
	origin := d.origins[index]
	if origin == nil {
		return nil
	}
	inherited := *origin
	return &inherited
}
