package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/nodepack"
	"github.com/kilaslab/kilas-flow/internal/property"
)

// credentialTypePackForValidate is a pack declaring its own credential type, in
// the pack directory layout `nodepackgen validate` reads.
func credentialTypePackForValidate() *nodepack.Pack {
	pack := nodepack.Scaffold(nodepack.ScaffoldOptions{
		Type: "pack.acme", DisplayName: "Acme", CredentialType: "pack.acme.api",
	})
	pack.CredentialTypes = []nodepack.CredentialType{{
		ID: "pack.acme.api", DisplayName: "Acme API",
		Fields: []nodepack.CredentialField{
			{Key: "baseUrl", Label: "Base URL", Kind: property.KindString, Required: true},
			{Key: "apiKey", Label: "API key", Kind: property.KindString, Required: true, Secret: true},
		},
		Authenticate:       &credentials.Authentication{Placement: credentials.PlacementHeader, Name: "X-Api-Key", Value: "{{ apiKey }}"},
		DefaultDomainsFrom: "baseUrl",
	}}
	return pack
}

// TestValidateReportsCredentialTypeProblemsWithTheirPaths: the author tool
// checks a pack's credentialTypes block and names each problem's JSON path, the
// way it does every other block.
func TestValidateReportsCredentialTypeProblemsWithTheirPaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	if err := nodepack.WritePackDir(good, credentialTypePackForValidate()); err != nil {
		t.Fatalf("WritePackDir(good) error = %v", err)
	}
	if issues := validatePath(good); len(issues) != 0 {
		t.Fatalf("validate(good) = %v, want no issues", issues)
	}

	bad := credentialTypePackForValidate()
	bad.CredentialType = "acmeApi"
	bad.CredentialTypes[0].ID = "acmeApi"
	bad.CredentialTypes[0].Authenticate.Value = "{{ token }}"
	badDir := filepath.Join(dir, "bad")
	if err := nodepack.WritePackDir(badDir, bad); err != nil {
		t.Fatalf("WritePackDir(bad) error = %v", err)
	}
	issues := validatePath(badDir)
	for _, want := range []struct{ path, message string }{
		{"$.credentialTypes[0].id", "outside this pack's namespace"},
		{"$.credentialTypes[0].authenticate.value", "{{ token }} names no field"},
	} {
		found := false
		for _, issue := range issues {
			found = found || (issue.Path == want.path && strings.Contains(issue.Message, want.message))
		}
		if !found {
			encoded, _ := json.MarshalIndent(issues, "", "  ")
			t.Errorf("validate(bad) = %s, want an issue at %s containing %q", encoded, want.path, want.message)
		}
	}
}
