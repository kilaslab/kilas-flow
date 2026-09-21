package sdk

import (
	"encoding/json"
	"errors"
)

// The errors a capability call answers with, so a pack can branch on what
// happened without reading a message:
//
//	if _, _, err := sdk.HTTP(request, body); errors.Is(err, sdk.ErrDeniedError) {
//		// the host refused it: the capability, the credential or the target
//	}
//
// They are sentinels rather than codes because a pack's own error handling
// should not have to know the wire numbers, and because a *HostError keeps the
// host's message beside the code for whoever reads the log.
var (
	// ErrInvalidError is a malformed argument or a host answer this SDK cannot
	// read.
	ErrInvalidError = errors.New("host call was invalid")
	// ErrDeniedError is a refusal: a capability not granted, a credential not
	// declared or not attached, a payload out of reach, a secret field.
	ErrDeniedError = errors.New("host call was denied")
	// ErrBlockedError is the egress policy refusing the target.
	ErrBlockedError = errors.New("host call was blocked")
	// ErrFailedError is a network, upstream or store failure.
	ErrFailedError = errors.New("host call failed")
	// ErrTooLargeError is an argument or a result beyond its cap.
	ErrTooLargeError = errors.New("host call was too large")
	// ErrNotFoundError is a named thing that does not exist.
	ErrNotFoundError = errors.New("host call found nothing")
)

// HTTP sends one request through the host and returns the response head and
// body.
//
// The host does everything that matters: it checks the URL against the
// deployment's SSRF policy, applies the credential named in request.Credential
// (which must be declared by the pack's manifest and attached to the node), and
// bounds the request's wall clock and response size. The pack supplies a
// description and gets bytes back; it cannot reach the network itself.
func HTTP(request HTTPRequest, body []byte) (HTTPResponse, []byte, error) {
	meta, err := json.Marshal(request)
	if err != nil {
		return HTTPResponse{}, nil, &HostError{Code: CodeInvalid, Message: "request could not be encoded: " + err.Error()}
	}
	if err := hostCallResult(callHTTP(meta, body)); err != nil {
		return HTTPResponse{}, nil, err
	}
	var response HTTPResponse
	if err := readSlotJSON(SlotResult, &response); err != nil {
		return HTTPResponse{}, nil, err
	}
	responseBody, err := readSlot(SlotBody)
	if err != nil {
		return HTTPResponse{}, nil, err
	}
	return response, responseBody, nil
}

// CredentialField reads one non-secret field of a credential the node attached.
//
// A secret field is refused: the host applies secrets to a request on the
// pack's behalf (HTTPRequest.Credential) and never puts one in guest memory,
// where a pack could log it or send it somewhere else.
func CredentialField(credentialType, field string) (string, error) {
	if err := hostCallResult(callCredentialField(credentialType, field)); err != nil {
		return "", err
	}
	var value string
	if err := readSlotJSON(SlotResult, &value); err != nil {
		return "", err
	}
	return value, nil
}

// ReadBinary reads a payload the input items carry, or one this run wrote.
//
// Any other reference is refused: a pack cannot read a payload by guessing its
// ID, which is what keeps the payload store out of reach of a guest that was
// handed one item.
func ReadBinary(ref BinaryRef) ([]byte, error) {
	if err := hostCallResult(callBinaryRead(ref.ID)); err != nil {
		return nil, err
	}
	return readSlot(SlotBody)
}

// WriteBinary stores a payload and returns the reference an output item may
// carry.
//
// The bytes go to the runtime's payload store, scoped to this execution's
// tenant, exactly as a built-in node's would.
func WriteBinary(name, mediaType string, data []byte) (BinaryRef, error) {
	meta, err := json.Marshal(BinaryWrite{Name: name, MediaType: mediaType})
	if err != nil {
		return BinaryRef{}, &HostError{Code: CodeInvalid, Message: "payload could not be described: " + err.Error()}
	}
	if err := hostCallResult(callBinaryWrite(meta, data)); err != nil {
		return BinaryRef{}, err
	}
	var ref BinaryRef
	if err := readSlotJSON(SlotResult, &ref); err != nil {
		return BinaryRef{}, err
	}
	return ref, nil
}

// hostCallResult turns a capability call's return into an error.
//
// A non-negative return is the length of the metadata now in SlotResult. A
// negative one is a refusal, and the host has written the code and the message
// into that same slot, so the error a pack sees carries the host's own words
// rather than a guess about them.
func hostCallResult(code int32) error {
	if code >= 0 {
		return nil
	}
	hostErr := &HostError{Code: ErrorCodeName(code)}
	body, err := readSlot(SlotResult)
	if err == nil && len(body) > 0 {
		// A refusal that cannot be decoded still has its code, which is the
		// part a caller branches on, so a decode failure is not fatal here.
		var reported HostError
		if json.Unmarshal(body, &reported) == nil && reported.Code != "" {
			hostErr = &reported
		}
	}
	if hostErr.Message == "" {
		hostErr.Message = "the host refused the call without a message"
	}
	return hostErr
}

// readSlot copies a result slot out of the host.
func readSlot(slot int32) ([]byte, error) {
	length := callResultLen(slot)
	if length < 0 {
		return nil, hostCallResult(length)
	}
	if length == 0 {
		return nil, nil
	}
	buffer := make([]byte, length)
	copied := callResultRead(slot, 0, buffer)
	if copied < 0 {
		return nil, hostCallResult(copied)
	}
	if copied > length {
		return nil, &HostError{Code: CodeInvalid, Message: "the host reported more bytes than the slot holds"}
	}
	return buffer[:copied], nil
}

// readSlotJSON copies a result slot out and decodes it.
func readSlotJSON(slot int32, target any) error {
	body, err := readSlot(slot)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return &HostError{Code: CodeInvalid, Message: "the host answered without a result"}
	}
	if err := json.Unmarshal(body, target); err != nil {
		return &HostError{Code: CodeInvalid, Message: "the host's answer could not be decoded: " + err.Error()}
	}
	return nil
}
