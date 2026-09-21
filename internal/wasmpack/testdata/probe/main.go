//go:build wasip1

// Command probe is the test fixture that drives every pack capability from one
// Go guest.
//
// It is one binary rather than one per capability because a Go wasip1 module
// costs seconds to translate under the race detector; the test binary builds it
// once, translates it once, and drives it with a JSON command on standard
// input. It is deliberately not a pack: it never uses the invocation envelope
// and it always exits zero, reporting what happened in its output document, so
// a test reads the host's answer rather than the sandbox's classification.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

// command is one probe instruction.
type command struct {
	Op              string            `json:"op"`
	URL             string            `json:"url,omitempty"`
	Method          string            `json:"method,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Credential      string            `json:"credential,omitempty"`
	FollowRedirects bool              `json:"followRedirects,omitempty"`
	TimeoutMS       int               `json:"timeoutMs,omitempty"`
	Body            string            `json:"body,omitempty"`

	CredentialType string `json:"credentialType,omitempty"`
	Field          string `json:"field,omitempty"`

	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
	Data      string `json:"data,omitempty"`
}

// result is what the probe reports.
type result struct {
	OK        bool                `json:"ok"`
	Error     string              `json:"error,omitempty"`
	ErrorCode string              `json:"errorCode,omitempty"`
	Status    int                 `json:"status,omitempty"`
	Headers   map[string][]string `json:"headers,omitempty"`
	Body      string              `json:"body,omitempty"`
	BodySize  int                 `json:"bodySize,omitempty"`
	Truncated bool                `json:"truncated,omitempty"`
	Value     string              `json:"value,omitempty"`
	Ref       *sdk.BinaryRef      `json:"ref,omitempty"`
	Data      string              `json:"data,omitempty"`
}

func main() {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		write(result{Error: "stdin could not be read: " + err.Error()})
		return
	}
	var instruction command
	if err := json.Unmarshal(input, &instruction); err != nil {
		write(result{Error: "the command could not be decoded: " + err.Error()})
		return
	}
	write(run(instruction))
}

func run(instruction command) result {
	switch instruction.Op {
	case "http":
		response, body, err := sdk.HTTP(sdk.HTTPRequest{
			Method:          instruction.Method,
			URL:             instruction.URL,
			Headers:         instruction.Headers,
			Credential:      instruction.Credential,
			FollowRedirects: instruction.FollowRedirects,
			TimeoutMS:       instruction.TimeoutMS,
		}, []byte(instruction.Body))
		if err != nil {
			return failure(err)
		}
		return result{
			OK: true, Status: response.Status, Headers: response.Headers,
			Body: string(body), BodySize: response.BodyLength, Truncated: response.Truncated,
		}
	case "credential":
		value, err := sdk.CredentialField(instruction.CredentialType, instruction.Field)
		if err != nil {
			return failure(err)
		}
		return result{OK: true, Value: value}
	case "read":
		payload, err := sdk.ReadBinary(sdk.BinaryRef{ID: instruction.ID})
		if err != nil {
			return failure(err)
		}
		return result{OK: true, Data: string(payload)}
	case "write":
		ref, err := sdk.WriteBinary(instruction.Name, instruction.MediaType, []byte(instruction.Data))
		if err != nil {
			return failure(err)
		}
		return result{OK: true, Ref: &ref}
	default:
		return result{Error: fmt.Sprintf("unknown op %q", instruction.Op)}
	}
}

// failure reports a refused capability call with the host's own code and
// message.
func failure(err error) result {
	var hostErr *sdk.HostError
	if errors.As(err, &hostErr) {
		return result{Error: hostErr.Message, ErrorCode: hostErr.Code}
	}
	return result{Error: err.Error()}
}

func write(report result) {
	encoded, err := json.Marshal(report)
	if err != nil {
		_, _ = os.Stdout.Write([]byte(`{"error":"the report could not be encoded"}`))
		return
	}
	_, _ = os.Stdout.Write(encoded)
}
