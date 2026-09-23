package jsworker

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// A frame is the protocol's unit: a JSON header and a raw blob, each
// length-prefixed. The blob carries what is big and already encoded, such as
// a job's input or a node's items, so it is never escaped into a JSON string.
//
// The server writes hello once, then one run per job. While the job runs the
// worker may write call, which the server answers with reply, and it ends the
// job with done.
type message struct {
	Type string `json:"type"`

	// hello: the deployment's ceiling, which every job is tightened to; the
	// live heap at which the worker's watchdog stops a script; and the
	// worker's address-space limit, where the platform has one.
	Limits       *jsrun.Limits `json:"limits,omitempty"`
	HeapCeiling  uint64        `json:"heapCeiling,omitempty"`
	AddressSpace uint64        `json:"addressSpace,omitempty"`

	// run: the job, whose input is the blob.
	Job *jsrun.Job `json:"job,omitempty"`

	// call: Method is "node" or "pair", asking for the node Name, or which of
	// its items the input item at Index descends from. reply: a node's view
	// is the blob and Found says there was one; a pairing is Index and
	// Reason.
	Method string `json:"method,omitempty"`
	Name   string `json:"name,omitempty"`
	Index  int    `json:"index"`
	Found  bool   `json:"found,omitempty"`
	Reason string `json:"reason,omitempty"`

	// done: what the job produced, and how it failed.
	Result *jsrun.Result    `json:"result,omitempty"`
	Error  *jsrun.WireError `json:"error,omitempty"`
}

const (
	typeHello = "hello"
	typeRun   = "run"
	typeCall  = "call"
	typeReply = "reply"
	typeDone  = "done"
)

// errFrameTooLarge reports a length prefix past what the reader allows, which
// only a broken or hostile peer writes.
var errFrameTooLarge = errors.New("jsworker: frame larger than allowed")

func writeFrame(w *bufio.Writer, m message, blob string) error {
	var header bytes.Buffer
	encoder := json.NewEncoder(&header)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(m); err != nil {
		return fmt.Errorf("jsworker: encoding a %s frame: %w", m.Type, err)
	}
	var prefix [8]byte
	binary.BigEndian.PutUint32(prefix[:4], uint32(header.Len()))
	binary.BigEndian.PutUint32(prefix[4:], uint32(len(blob)))
	if _, err := w.Write(prefix[:]); err != nil {
		return err
	}
	if _, err := w.Write(header.Bytes()); err != nil {
		return err
	}
	if _, err := w.WriteString(blob); err != nil {
		return err
	}
	return w.Flush()
}

// readFrame reads one frame, refusing a header or blob longer than its limit
// before allocating for it. A stream that ends between frames is io.EOF; one
// that ends inside a frame is io.ErrUnexpectedEOF.
func readFrame(r *bufio.Reader, headerLimit, blobLimit int64) (message, []byte, error) {
	var prefix [8]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return message{}, nil, err
	}
	headerSize, blobSize := int64(binary.BigEndian.Uint32(prefix[:4])), int64(binary.BigEndian.Uint32(prefix[4:]))
	if headerSize > headerLimit || blobSize > blobLimit {
		return message{}, nil, errFrameTooLarge
	}
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return message{}, nil, unexpected(err)
	}
	blob := make([]byte, blobSize)
	if _, err := io.ReadFull(r, blob); err != nil {
		return message{}, nil, unexpected(err)
	}
	var m message
	if err := json.Unmarshal(header, &m); err != nil {
		return message{}, nil, fmt.Errorf("jsworker: decoding a frame: %w", err)
	}
	return m, blob, nil
}

func unexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}
