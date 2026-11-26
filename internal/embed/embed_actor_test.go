package embed_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/embed"
)

func TestAnActorIsSignedIntoTheTokenAndVerifiedBack(t *testing.T) {
	t.Parallel()

	issuer := newIssuer(t, nil)
	request := validRequest()
	request.Actor = &embed.Actor{ID: "auth0|abc123", DisplayName: "José O'Brien"}
	session, token, err := issuer.Issue(request)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if session.Actor == nil || *session.Actor != *request.Actor {
		t.Fatalf("issued actor = %#v, want %#v", session.Actor, request.Actor)
	}
	// The session holds its own copy: a caller reusing its request value
	// cannot rewrite the actor of a session already minted.
	request.Actor.DisplayName = "Mallory"
	if session.Actor.DisplayName != "José O'Brien" {
		t.Errorf("issued actor changed with the request: %#v", session.Actor)
	}

	verified, err := issuer.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if verified.Actor == nil || verified.Actor.ID != "auth0|abc123" || verified.Actor.DisplayName != "José O'Brien" {
		t.Fatalf("verified actor = %#v, want the signed one", verified.Actor)
	}
}

func TestASessionWithoutAnActorCarriesNone(t *testing.T) {
	t.Parallel()

	issuer := newIssuer(t, nil)
	session, token, err := issuer.Issue(validRequest())
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if session.Actor != nil {
		t.Errorf("issued actor = %#v, want nil", session.Actor)
	}
	verified, err := issuer.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if verified.Actor != nil {
		t.Errorf("verified actor = %#v, want nil", verified.Actor)
	}
}

func TestActorValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		actor embed.Actor
		ok    bool
	}{
		{"empty id", embed.Actor{ID: ""}, false},
		{"id over 128 characters", embed.Actor{ID: strings.Repeat("a", embed.MaxActorIDLength+1)}, false},
		{"id with a space", embed.Actor{ID: "user 1"}, false},
		{"id with markup", embed.Actor{ID: "<script>"}, false},
		{"name over 64 runes", embed.Actor{ID: "u1", DisplayName: strings.Repeat("é", embed.MaxActorDisplayNameLength+1)}, false},
		{"name with markup", embed.Actor{ID: "u1", DisplayName: "<b>x</b>"}, false},
		{"name with a newline", embed.Actor{ID: "u1", DisplayName: "Ada\nLovelace"}, false},
		{"name with a leading space", embed.Actor{ID: "u1", DisplayName: " Ada"}, false},
		{"name with a trailing space", embed.Actor{ID: "u1", DisplayName: "Ada "}, false},
		{"pipe-separated provider id", embed.Actor{ID: "auth0|abc"}, true},
		{"email id", embed.Actor{ID: "ada@example.com", DisplayName: "Ada Lovelace"}, true},
		{"id at the limit", embed.Actor{ID: strings.Repeat("a", embed.MaxActorIDLength)}, true},
		{"accented name with apostrophe", embed.Actor{ID: "u1", DisplayName: "José O'Brien"}, true},
		{"non-latin name", embed.Actor{ID: "u1", DisplayName: "李小龙"}, true},
		{"name at the limit", embed.Actor{ID: "u1", DisplayName: strings.Repeat("é", embed.MaxActorDisplayNameLength)}, true},
		{"no display name", embed.Actor{ID: "u1"}, true},
	}
	issuer := newIssuer(t, nil)
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			err := testCase.actor.Validate()
			if testCase.ok && err != nil {
				t.Fatalf("Validate() error = %v, want accepted", err)
			}
			if !testCase.ok && err == nil {
				t.Fatalf("Validate() accepted %#v, want refused", testCase.actor)
			}
			// Issue applies the same rule: a refused actor is never minted.
			request := validRequest()
			actor := testCase.actor
			request.Actor = &actor
			_, _, issueErr := issuer.Issue(request)
			if (issueErr == nil) != testCase.ok {
				t.Fatalf("Issue() error = %v, want accepted=%v", issueErr, testCase.ok)
			}
		})
	}
}
