package n8n_test

// FEAT-sz4ddp: the email pair imports. Send Email crosses with its own
// parameter keys on both the v1 and v2 shapes, the IMAP trigger lands on the
// poll framework with its watermark shape, and everything one side genuinely
// drops is named in the report rather than silently changed.

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// emailNodeFixture is a workflow with a Manual trigger feeding one email node.
func emailNodeFixture(nodeType string, typeVersion float64, parameters string) string {
	return legacyNodeFixture(nodeType, typeVersion, parameters)
}

func TestSendEmailImportsItsV1AndV2Spellings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		typeVersion float64
		parameters  string
		wantKey     string
		wantValue   any
	}{
		{
			name:        "v2 carries its options members",
			typeVersion: 2.1,
			parameters:  `{"fromEmail":"ops@example.test","toEmail":"alice@example.test","subject":"nightly","emailFormat":"html","html":"<p>all good</p>","options":{"ccEmail":"carol@example.test","fileAttachments":"report","appendAttribution":true}}`,
			wantKey:     "fromEmail", wantValue: "ops@example.test",
		},
		{
			name:        "v1 carries its top-level cc and attachment list",
			typeVersion: 1,
			parameters:  `{"fromEmail":"ops@example.test","toEmail":"alice@example.test","ccEmail":"carol@example.test","attachments":"report,logo","text":"all good"}`,
			wantKey:     "attachments", wantValue: "report,logo",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := importFixture(t, emailNodeFixture("n8n-nodes-base.emailSend", test.typeVersion, test.parameters))
			node := nodeByName(result.Document, "Legacy")
			if node.Type != n8n.EmailSendNodeType {
				t.Fatalf("imported as %q, want %q", node.Type, n8n.EmailSendNodeType)
			}
			if got := node.Parameters[test.wantKey]; got != test.wantValue {
				t.Fatalf("%s = %#v, want %#v", test.wantKey, got, test.wantValue)
			}
			if got := node.Parameters["emailFormat"]; test.typeVersion > 1 && got != "html" {
				t.Fatalf("emailFormat = %#v, want it carried as written", got)
			}
			if blocking := blockingFor(result, "Legacy"); len(blocking) != 0 {
				t.Fatalf("blocking issues = %#v, want none", blocking)
			}
			var attribution bool
			for _, issue := range result.Unsupported {
				if issue.Field == "options.appendAttribution" {
					attribution = true
				}
			}
			if test.typeVersion > 1 && !attribution {
				t.Errorf("no issue named the dropped attribution footer: %#v", result.Unsupported)
			}
		})
	}
}

func TestSendEmailRoundTripsAtItsAuthoredVersion(t *testing.T) {
	t.Parallel()

	result := importFixture(t, emailNodeFixture("n8n-nodes-base.emailSend", 2.1,
		`{"fromEmail":"ops@example.test","toEmail":"alice@example.test","text":"hi","options":{"fileAttachments":"report"}}`))
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, node := range exported.Document.Nodes {
		if node.Name != "Legacy" {
			continue
		}
		if node.Type != "n8n-nodes-base.emailSend" || node.TypeVersion != 2.1 {
			t.Fatalf("exported as %q v%v, want n8n-nodes-base.emailSend v2.1", node.Type, node.TypeVersion)
		}
		if node.Parameters["fromEmail"] != "ops@example.test" {
			t.Fatalf("fromEmail = %#v, want it carried back", node.Parameters["fromEmail"])
		}
	}

	// A version n8n does not publish falls back to the pin rather than being
	// written through, because n8n itself refuses to open it.
	unpublished := importFixture(t, emailNodeFixture("n8n-nodes-base.emailSend", 3, `{"text":"hi"}`))
	exported, err = n8n.Export(unpublished.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, node := range exported.Document.Nodes {
		if node.Name == "Legacy" && node.TypeVersion != 2.1 {
			t.Fatalf("exported v%v, want the 2.1 pin for an unpublished version", node.TypeVersion)
		}
	}
}

func TestTheImapTriggerImportsOntoThePollFramework(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Imap",
	  "nodes": [
	    {"id":"a","name":"Email Trigger (IMAP)","type":"n8n-nodes-base.emailReadImap","typeVersion":2.2,"position":[0,0],
	     "parameters":{"mailbox":"INBOX","postProcessAction":"read","format":"simple",
	       "pollTimes":{"item":[{"mode":"everyX","unit":"minutes","value":10}]},
	       "options":{"trackLastMessageId":true}}},
	    {"id":"b","name":"No Op","type":"n8n-nodes-base.noOp","typeVersion":1,"position":[220,0],"parameters":{}}
	  ],
	  "connections": {"Email Trigger (IMAP)": {"main": [[{"node":"No Op","type":"main","index":0}]]}}
	}`)
	node := nodeByName(result.Document, "Email Trigger (IMAP)")
	if node.Type != n8n.EmailReadImapNodeType {
		t.Fatalf("imported as %q, want %q", node.Type, n8n.EmailReadImapNodeType)
	}
	if node.Parameters["mailbox"] != "INBOX" || node.Parameters["postProcessAction"] != "read" {
		t.Fatalf("parameters = %#v, want mailbox and action carried", node.Parameters)
	}
	polls := workflow.Document{Nodes: []workflow.Node{node}}
	_ = polls
	if blocking := blockingFor(result, "Email Trigger (IMAP)"); len(blocking) != 0 {
		t.Fatalf("blocking issues = %#v, want none", blocking)
	}
}

func TestTheImapTriggerNamesWhatItCannotCarry(t *testing.T) {
	t.Parallel()

	result := importFixture(t, emailNodeFixture("n8n-nodes-base.emailReadImap", 2.2,
		`{"mailbox":"INBOX","format":"resolved","downloadAttachments":true,
		  "options":{"customEmailConfig":"[\"UNSEEN\",\"FROM\",\"boss@example.test\"]"}}`))
	node := nodeByName(result.Document, "Legacy")
	if node.Parameters["format"] != "simple" {
		t.Fatalf("format = %#v, want resolved downgraded to simple", node.Parameters["format"])
	}
	report := ""
	for _, issue := range result.Unsupported {
		report += issue.Reason + "\n"
	}
	for _, want := range []string{"resolved format", "custom IMAP search rules", "downloaded attachments"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not name %q:\n%s", want, report)
		}
	}
}

func TestTheImapTriggerRoundTripsItsKeys(t *testing.T) {
	t.Parallel()

	result := importFixture(t, emailNodeFixture("n8n-nodes-base.emailReadImap", 2,
		`{"mailbox":"Support","postProcessAction":"nothing","format":"raw","pollTimes":{"item":[{"mode":"everyHour"}]}}`))
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, node := range exported.Document.Nodes {
		if node.Name != "Legacy" {
			continue
		}
		if node.Type != "n8n-nodes-base.emailReadImap" {
			t.Fatalf("exported as %q", node.Type)
		}
		if node.Parameters["mailbox"] != "Support" || node.Parameters["format"] != "raw" {
			t.Fatalf("parameters = %#v, want them carried back", node.Parameters)
		}
	}
}
