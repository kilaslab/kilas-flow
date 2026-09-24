package jsrun

import (
	"bytes"
	"encoding/json"
	"fmt"
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
				wire[index].Binary[property] = fileOfRef(ref)
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
		where := whereReturned(index, eachItem)
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

// checkFiles refuses a result that passes on a file the node was not given,
// or one it did not store itself.
// It runs where the code ran, after each call, so a per-item run stops (or,
// continuing on failure, fails) at the item that returned it; decoding in the
// server checks again. Most results carry no file at all, which the text
// shows without decoding it: the runtime writes a returned item's binary as
// a non-empty object only when there is one.
func checkFiles(text string, known map[string]bool, eachItem bool) error {
	if !strings.Contains(text, `"binary":{"`) {
		return nil
	}
	var wire []struct {
		Binary map[string]json.RawMessage `json:"binary"`
	}
	if err := json.Unmarshal([]byte(text), &wire); err != nil {
		return fmt.Errorf("jsrun: decoding the code's result: %w", err)
	}
	for index, returned := range wire {
		for property, raw := range returned.Binary {
			if _, err := fileOf(raw, known, whereReturned(index, eachItem), property); err != nil {
				return err
			}
		}
	}
	return nil
}

// fileOf reads one returned binary entry, which may only name a file the
// node was given.
func fileOf(raw json.RawMessage, known map[string]bool, where, property string) (wireBinary, error) {
	var file wireBinary
	if json.Unmarshal(raw, &file) != nil || file.ID == "" {
		return file, named(ErrInvalidReturn, fmt.Sprintf("%s has a binary %q that is not a file reference; binary entries can only pass on files the node was given or stored with prepareBinaryData", where, property))
	}
	if !known[file.ID] {
		return file, named(ErrInvalidReturn, fmt.Sprintf("%s has a binary %q naming a file this node was not given and did not store", where, property))
	}
	return file, nil
}

func whereReturned(index int, eachItem bool) string {
	if eachItem {
		return "the returned value"
	}
	return fmt.Sprintf("item %d", index)
}

// binary maps each returned file onto the input's. Binary is passed on only
// when the code returns it, as n8n does; the Go Code node's positional
// carry-over does not apply here.
func (d *decoder) binary(returned map[string]json.RawMessage, where string) (map[string]workflow.BinaryRef, error) {
	if len(returned) == 0 {
		return nil, nil
	}
	known := make(map[string]bool, len(d.files))
	for id := range d.files {
		known[id] = true
	}
	refs := make(map[string]workflow.BinaryRef, len(returned))
	for property, raw := range returned {
		file, err := fileOf(raw, known, where, property)
		if err != nil {
			return nil, err
		}
		ref := d.files[file.ID]
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
