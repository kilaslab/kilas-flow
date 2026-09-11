package n8n_test

import (
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/interop/n8n"
)

func TestGOWAAppDevicesImportsAsHTTPNotUnsupported(t *testing.T) {
	payload := []byte(`{
	  "name":"My workflow",
	  "nodes":[
	    {"id":"m","name":"When clicking","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"parameters":{}},
	    {"id":"g","name":"Get device information","type":"@aldinokemal2104/n8n-nodes-gowa.gowa","typeVersion":1,
	     "parameters":{"resource":"app"},
	     "credentials":{"goWhatsappApi":{"id":"u","name":"GOWA"}}}
	  ],
	  "connections":{"When clicking":{"main":[[{"node":"Get device information","type":"main","index":0}]]}}
	}`)
	result, err := n8n.Import(payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, node := range result.Document.Nodes {
		if node.Name != "Get device information" {
			continue
		}
		found = true
		if node.Type != "kilasflow.httpRequest" {
			t.Fatalf("type = %q, want kilasflow.httpRequest (got unsupported?)", node.Type)
		}
		url, _ := node.Parameters["url"].(string)
		if !strings.Contains(url, "/app/devices") {
			t.Fatalf("url = %q, want …/app/devices", url)
		}
		method, _ := node.Parameters["method"].(string)
		if method != "GET" {
			t.Fatalf("method = %q", method)
		}
	}
	if !found {
		t.Fatal("GOWA node missing")
	}
	for _, u := range result.Unsupported {
		if u.Severity == n8n.SeverityBlocking && u.NodeName == "Get device information" {
			t.Fatalf("blocking unsupported remained: %+v", u)
		}
		if u.Type == "@aldinokemal2104/n8n-nodes-gowa.gowa" && u.Severity == n8n.SeverityBlocking {
			t.Fatalf("still blocking on GOWA type: %+v", u)
		}
	}
}

func TestGOWASendMessageMapsPOST(t *testing.T) {
	payload := []byte(`{
	  "name":"Send",
	  "nodes":[
	    {"id":"g","name":"Send Text","type":"@aldinokemal2104/n8n-nodes-gowa.gowa","typeVersion":1,
	     "parameters":{"resource":"send","operation":"message","phone":"628@s.whatsapp.net","message":"hi"}}
	  ],
	  "connections":{}
	}`)
	result, err := n8n.Import(payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	node := result.Document.Nodes[0]
	if node.Type != "kilasflow.httpRequest" {
		t.Fatalf("type = %q", node.Type)
	}
	if node.Parameters["method"] != "POST" {
		t.Fatalf("method = %#v", node.Parameters["method"])
	}
	url, _ := node.Parameters["url"].(string)
	if !strings.HasSuffix(url, "/send/message") {
		t.Fatalf("url = %q", url)
	}
}
