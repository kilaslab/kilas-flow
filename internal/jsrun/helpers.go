package jsrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// What a body asks of the server beyond its own input: this.helpers'
// httpRequest, getBinaryDataBuffer and prepareBinaryData, and
// $getWorkflowStaticData.
//
// The work is the server's, never the VM's process: a request is sent by the
// server under the deployment's egress policy, and a file is read from or
// stored in the execution's own storage. The runtime turns n8n's arguments
// into a HostRequest, one asynchronous host call answers it, and the answer
// settles the promise the code holds, on the execution's own job loop, so the
// code goes on running timers and other promises while it waits.

const (
	// MaxFileBytes bounds what one helper call may move: a file read or
	// stored, or a request body. It is below MaxBytesPerCall, so a file that
	// is allowed across fits in a Buffer.
	MaxFileBytes = 32 << 20
	// MaxStaticDataBytes bounds a workflow's static data, as JSON.
	MaxStaticDataBytes = 256 << 10
)

// The helper methods a HostRequest names, which are n8n's own helper names.
const (
	HelperHTTPRequest   = "httpRequest"
	HelperReadFile      = "getBinaryDataBuffer"
	HelperWriteFile     = "prepareBinaryData"
	staticDataGlobal    = "global"
	staticDataNodeScope = "node"
)

// Helpers is the server's half of the helpers and of the static data. The
// node that runs the code provides it, bound to its own request, so nothing
// it answers can reach another tenant's data. A nil Helpers leaves every
// helper failing as unavailable, and the static data empty and unsaved.
type Helpers interface {
	// HTTPRequest sends one request and returns the response, whatever its
	// status, with its body. An error that is a named limit (errors.As a
	// *LimitError) stops the code; any other rejects its promise.
	HTTPRequest(ctx context.Context, request HTTPRequest, body []byte) (HTTPResponse, []byte, error)
	// ReadFile returns the bytes of the file the node's input item itemIndex
	// holds under property.
	ReadFile(ctx context.Context, itemIndex int, property string) ([]byte, error)
	// WriteFile stores data as a file of this execution, unless ctx has
	// ended by the time it would store it.
	WriteFile(ctx context.Context, data []byte, fileName, mimeType string) (workflow.BinaryRef, error)
	// StaticData returns the workflow's static data of one kind, "global" or
	// "node", as a JSON object.
	StaticData(kind string) (string, error)
}

// HTTPRequest is one request as the runtime built it from n8n's options:
// the query is already in the URL and the body travels beside it.
type HTTPRequest struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	// Headers are sent as given, in order.
	Headers [][2]string `json:"headers,omitempty"`
	// TimeoutMS tightens the policy's timeout, in milliseconds; 0 keeps it.
	TimeoutMS int64 `json:"timeout,omitempty"`
	// Redirects is how many redirects to follow, never more than the
	// policy's; nil follows the policy's.
	Redirects *int `json:"redirects,omitempty"`
}

// HTTPResponse is a response's status line and headers, as n8n reports them:
// header names in lower case, a repeated header as a list.
type HTTPResponse struct {
	StatusCode    int            `json:"statusCode"`
	StatusMessage string         `json:"statusMessage"`
	Headers       map[string]any `json:"headers"`
}

// HostRequest is one asynchronous call the code makes of the server.
type HostRequest struct {
	// Method is the helper: HelperHTTPRequest, HelperReadFile or
	// HelperWriteFile.
	Method string       `json:"method"`
	HTTP   *HTTPRequest `json:"http,omitempty"`
	// ItemIndex and Property name the input file HelperReadFile reads.
	ItemIndex int    `json:"itemIndex,omitempty"`
	Property  string `json:"property,omitempty"`
	// FileName and MimeType describe the file HelperWriteFile stores.
	FileName string `json:"fileName,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	// Data is the request body, or the file to store. It is the bulk of a
	// request, so it travels beside the rest.
	Data []byte `json:"-"`
}

// HostAnswer is the server's answer to a HostRequest.
type HostAnswer struct {
	HTTP *HTTPResponse `json:"http,omitempty"`
	// File is the file HelperWriteFile stored, as the code sees files.
	File *wireBinary `json:"file,omitempty"`
	// Data is a response body or a file's bytes, beside the rest.
	Data []byte `json:"-"`
	// Failure rejects the code's promise with an Error saying it.
	Failure string `json:"failure,omitempty"`
	// Error is a named limit, which stops the code.
	Error *WireError `json:"error,omitempty"`
}

// failed answers with err: a named limit stops the code, anything else
// rejects its promise.
func failed(err error) HostAnswer {
	var limit *LimitError
	if errors.As(err, &limit) {
		return HostAnswer{Error: EncodeError(err)}
	}
	return HostAnswer{Failure: err.Error()}
}

// ledger is what a job's host handed out: the files the code stored and the
// kinds of static data it read. Finish accepts a file or static data in a
// result only as far as the ledger says the code could have it, whoever ran
// the code.
type ledger struct {
	mu     sync.Mutex
	files  map[string]workflow.BinaryRef
	static map[string]bool
}

func newLedger() *ledger {
	return &ledger{files: map[string]workflow.BinaryRef{}, static: map[string]bool{}}
}

func (l *ledger) stored(ref workflow.BinaryRef) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.files[ref.ID] = ref
}

func (l *ledger) read(kind string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.static[kind] = true
}

// knownFiles is the input's files and the ones the code stored.
func (l *ledger) knownFiles(input map[string]workflow.BinaryRef) map[string]workflow.BinaryRef {
	if l == nil {
		return input
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.files) == 0 {
		return input
	}
	known := make(map[string]workflow.BinaryRef, len(input)+len(l.files))
	for id, ref := range input {
		known[id] = ref
	}
	for id, ref := range l.files {
		known[id] = ref
	}
	return known
}

func (l *ledger) handedOut(kind string) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.static[kind]
}

// call answers one HostRequest from the node's helpers, recording what it
// hands out. The caller runs it on a goroutine of its own.
func (l *ledger) call(helpers Helpers) func(context.Context, HostRequest) HostAnswer {
	return func(ctx context.Context, request HostRequest) HostAnswer {
		if helpers == nil {
			return HostAnswer{Failure: "this.helpers." + request.Method + " is not available here"}
		}
		// A call the code left behind when its run ended does nothing.
		if ctx.Err() != nil {
			return HostAnswer{Failure: "the run this call belongs to is over"}
		}
		switch request.Method {
		case HelperHTTPRequest:
			if request.HTTP == nil {
				return HostAnswer{Failure: "httpRequest was called without a request"}
			}
			if size := int64(len(request.Data)); size > MaxFileBytes {
				return failed(FileLimitError("a request body", size))
			}
			response, body, err := helpers.HTTPRequest(ctx, *request.HTTP, request.Data)
			if err != nil {
				return failed(err)
			}
			return HostAnswer{HTTP: &response, Data: body}
		case HelperReadFile:
			data, err := helpers.ReadFile(ctx, request.ItemIndex, request.Property)
			if err != nil {
				return failed(err)
			}
			if size := int64(len(data)); size > MaxFileBytes {
				return failed(FileLimitError("a file", size))
			}
			return HostAnswer{Data: data}
		case HelperWriteFile:
			if size := int64(len(request.Data)); size > MaxFileBytes {
				return failed(FileLimitError("a file", size))
			}
			// The node's WriteFile checks ctx again right before it stores,
			// so a run that ends meanwhile leaves no file behind.
			stored, err := helpers.WriteFile(ctx, request.Data, request.FileName, request.MimeType)
			if err != nil {
				return failed(err)
			}
			l.stored(stored)
			file := fileOfRef(stored)
			return HostAnswer{File: &file}
		}
		return HostAnswer{Failure: fmt.Sprintf("there is no helper %q", request.Method)}
	}
}

// staticData answers $getWorkflowStaticData from the node's helpers,
// recording which kinds the code was handed. Without helpers the data starts
// empty and goes nowhere.
func (l *ledger) staticData(helpers Helpers) func(string) (string, error) {
	return func(kind string) (string, error) {
		if kind != staticDataGlobal && kind != staticDataNodeScope {
			return "", fmt.Errorf("there is no static data of kind %q", kind)
		}
		text := "{}"
		if helpers != nil {
			var err error
			if text, err = helpers.StaticData(kind); err != nil {
				return "", err
			}
		}
		l.read(kind)
		return text, nil
	}
}

// checkStaticData takes the static data a run handed back: only kinds the
// code was handed, each a JSON object within the cap.
func (job Job) checkStaticData(returned map[string]string) (map[string]string, error) {
	if len(returned) == 0 {
		return nil, nil
	}
	checked := make(map[string]string, len(returned))
	for kind, text := range returned {
		if !job.ledger.handedOut(kind) {
			return nil, EngineFaultError(fmt.Sprintf("it returned static data of kind %q, which the code was never given", kind))
		}
		if len(text) > MaxStaticDataBytes {
			return nil, StaticDataLimitError()
		}
		var object map[string]any
		if !strings.HasPrefix(text, "{") || json.Unmarshal([]byte(text), &object) != nil {
			return nil, EngineFaultError("it returned static data that is not a JSON object")
		}
		checked[kind] = text
	}
	return checked, nil
}

// fileOfRef is a stored file as the code sees it.
func fileOfRef(ref workflow.BinaryRef) wireBinary {
	return wireBinary{
		ID: ref.ID, FileName: ref.FileName, MimeType: ref.MediaType,
		FileExtension: strings.TrimPrefix(path.Ext(ref.FileName), "."), FileSize: ref.Size,
	}
}
