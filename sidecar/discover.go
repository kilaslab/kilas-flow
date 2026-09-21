package sidecar

import (
	"context"
	"encoding/json"
	"time"
)

// Discover spawns one sidecar process in the describe role, asks for the
// catalogue of packages it loaded, and reaps it. The process is started with
// an empty tenant and can never serve a run: Pool.Execute rejects an empty
// tenant before it looks at the map, so a describe process is unreachable
// from a workflow.
//
// This is how the host learns what a package directory contains before it
// wires any executor: the load happens in the child, under the same
// permission model and limits as a real run.
func Discover(ctx context.Context, spawn SpawnFunc, limits Limits) (json.RawMessage, error) {
	limits = normalizeLimits(limits)
	if spawn == nil {
		return nil, &CallError{Code: CodeNoSidecar, Detail: "this deployment has no JavaScript sidecar: install Node 24 LTS and the node's package, then point the deployment at it"}
	}
	spawnCtx, cancel := context.WithTimeout(ctx, limits.SpawnTimeout)
	defer cancel()
	child, err := spawn(spawnCtx, "", "")
	if err != nil {
		return nil, err
	}
	if child.Kill != nil {
		defer func() { _ = child.Kill() }()
	}
	if child.Conn == nil {
		return nil, &CallError{Code: CodeSpawnFailed, Detail: "the sidecar described no socket connection"}
	}

	id := newCallID()
	if err := writeFrame(child.Conn, describeFrame{Type: frameDescribe, ID: id}); err != nil {
		return nil, &CallError{Code: CodeSidecarCrash, Detail: "the sidecar catalogue could not be requested"}
	}
	if err := child.Conn.SetDeadline(time.Now().Add(limits.Timeout)); err != nil {
		return nil, &CallError{Code: CodeSidecarCrash, Detail: "the sidecar socket refused a deadline"}
	}

	reader := newFrameReader(child.Conn, limits.MaxFrameBytes)
	for {
		line, err := reader.next()
		if err != nil {
			if isTimeout(err) {
				return nil, &CallError{Code: CodeSidecarTimeout, Detail: "the sidecar did not describe its packages in time"}
			}
			return nil, &CallError{Code: CodeSidecarCrash, Detail: "the sidecar stopped before describing its packages"}
		}
		answer, err := decodeTerminal(line)
		if err != nil {
			return nil, err
		}
		if answer.ID != id {
			return nil, &CallError{Code: CodeProtocolViolation, Detail: "sidecar described a run that was never sent"}
		}
		if answer.Type == frameError {
			return nil, &CallError{Code: CodeSidecarCrash, Detail: "sidecar refused to describe its packages: " + truncate(answer.Message, maxChildMessage)}
		}
		if answer.Catalogue == nil {
			return nil, &CallError{Code: CodeProtocolViolation, Detail: "the sidecar answered describe without a catalogue"}
		}
		if int64(len(answer.Catalogue)) > int64(limits.MaxCatalogueBytes) {
			return nil, &CallError{Code: CodeOutputTooLarge, Detail: "the sidecar catalogue is larger than the limit allows"}
		}
		return answer.Catalogue, nil
	}
}
