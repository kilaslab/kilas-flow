package cli

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// An agent reports which skills informed a write, and the server records them
// on the revision the call creates. The header is what carries them, so it is
// sent on a write and only on a write: a read changes nothing for the report to
// describe.
func TestTheSkillsUsedHeaderRidesOnMutatingRequestsOnly(t *testing.T) {
	var (
		gotPut  string
		gotGet  string
		putSeen bool
	)

	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/workflows/wf_1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut {
				putSeen = true
				gotPut = r.Header.Get(skillsUsedHeader)
			} else {
				gotGet = r.Header.Get(skillsUsedHeader)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		},
	})

	client := &Client{
		BaseURL: srv.URL, Token: testToken,
		HTTP: &http.Client{Timeout: time.Second}, Now: time.Now,
		SkillsUsed: []string{"kilasflow-debugging", "kilasflow-expressions"},
	}

	if _, err := client.Do(context.Background(), http.MethodPut, apiPrefix+"/workflows/wf_1", nil, nil, []byte(`{}`)); err != nil {
		t.Fatalf("Do(PUT): %v", err)
	}
	if !putSeen {
		t.Fatal("the PUT never reached the server")
	}
	if got, want := gotPut, "kilasflow-debugging,kilasflow-expressions"; got != want {
		t.Errorf("X-KilasFlow-Skills-Used on a write = %q, want %q", got, want)
	}

	if _, err := client.Do(context.Background(), http.MethodGet, apiPrefix+"/workflows/wf_1", nil, nil, nil); err != nil {
		t.Fatalf("Do(GET): %v", err)
	}
	if got := gotGet; got != "" {
		t.Errorf("X-KilasFlow-Skills-Used on a read = %q, want none", got)
	}

	// A client nobody told anything about sends no header at all, so an
	// ordinary invocation is byte-identical to what it was before.
	quiet := &Client{
		BaseURL: srv.URL, Token: testToken,
		HTTP: &http.Client{Timeout: time.Second}, Now: time.Now,
	}
	gotPut = ""
	if _, err := quiet.Do(context.Background(), http.MethodPut, apiPrefix+"/workflows/wf_1", nil, nil, []byte(`{}`)); err != nil {
		t.Fatalf("Do(quiet PUT): %v", err)
	}
	if gotPut != "" {
		t.Errorf("X-KilasFlow-Skills-Used from a client that reported nothing = %q, want none", gotPut)
	}
}

// --skills-used is comma-separated and forgiving about the spaces a caller puts
// around the names, because an agent composing the flag by hand will.
func TestTheSkillsUsedFlagIsSplitAndTrimmed(t *testing.T) {
	flags := &GlobalFlags{SkillsUsed: " kilasflow-debugging , ,kilasflow-expressions "}
	client := newClient(flags, "http://127.0.0.1:1", "")

	want := []string{"kilasflow-debugging", "kilasflow-expressions"}
	if len(client.SkillsUsed) != len(want) {
		t.Fatalf("SkillsUsed = %v, want %v", client.SkillsUsed, want)
	}
	for i, name := range want {
		if client.SkillsUsed[i] != name {
			t.Errorf("SkillsUsed[%d] = %q, want %q", i, client.SkillsUsed[i], name)
		}
	}
}
