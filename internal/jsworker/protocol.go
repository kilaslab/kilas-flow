package jsworker

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// A frame is the protocol's unit: a JSON header and a raw blob, each
// length-prefixed. The blob carries what is big and already encoded, such as
// a job's input, a node's items or the code's results, so it is never escaped
// into a JSON string.
//
// The server writes hello once, then one run per job. While the job runs the
// worker may write call, which the server answers with reply, and it ends the
// job with done. Every frame of a job carries the job's nonce, so a frame
// left over from another job is refused rather than taken for this one's.
type message struct {
	Type  string `json:"type"`
	Nonce string `json:"nonce,omitempty"`

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

	// done: what the job produced, whose results are the blob, cut at
	// OutputSizes, and how it failed.
	Executed    *jsrun.Executed  `json:"executed,omitempty"`
	OutputSizes []int            `json:"outputSizes,omitempty"`
	Error       *jsrun.WireError `json:"error,omitempty"`
}

const (
	typeHello = "hello"
	typeRun   = "run"
	typeCall  = "call"
	typeReply = "reply"
	typeDone  = "done"
)

// protocolError is a peer that broke the protocol: a frame too large, one
// that does not decode, or one out of turn. Only a broken or hostile peer
// writes one.
type protocolError struct{ reason string }

func (e *protocolError) Error() string { return "jsworker: " + e.reason }

func protocolViolation(format string, args ...any) error {
	return &protocolError{reason: fmt.Sprintf(format, args...)}
}

func writeFrame(w *bufio.Writer, m message, blob string) error {
	var header bytes.Buffer
	encoder := json.NewEncoder(&header)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(m); err != nil {
		return fmt.Errorf("jsworker: encoding a %s frame: %w", m.Type, err)
	}
	if header.Len() > math.MaxUint32 || len(blob) > math.MaxUint32 {
		return fmt.Errorf("jsworker: a %s frame is too large to send", m.Type)
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
		return message{}, nil, protocolViolation("a frame of %d+%d bytes, past the %d+%d allowed", headerSize, blobSize, headerLimit, blobLimit)
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
		return message{}, nil, protocolViolation("a frame that does not decode: %v", err)
	}
	return m, blob, nil
}

func unexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}
