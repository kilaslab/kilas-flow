package nodes_test

import (
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/nodes"
)

// BUG-epy2se: an OpenAI credential carries n8n's Base URL (`url`), and a chat
// model node that leaves its own base URL at the provider default calls that
// address instead, so an OpenAI-compatible endpoint configured once on the
// credential is what every node using it reaches.
func TestAnOpenAIChatModelFallsBackToItsCredentialsBaseURL(t *testing.T) {
	t.Parallel()

	const ollama = "http://ollama.internal.test:11434/v1"
	withURL := &stubCredentials{credential: engine.Credential{
		ID: "cred-key", Name: "Ollama", Type: nodes.OpenAICredentialType,
		Fields: map[string]string{"apiKey": "ollama", "url": ollama},
	}}
	for _, test := range []struct {
		name     string
		baseURL  any
		resolver *stubCredentials
		want     string
	}{
		{"node base URL absent", nil, withURL, ollama},
		{"node base URL empty", "", withURL, ollama},
		// The node's declared default is filled into every stored document,
		// so it reads as "not set" rather than as a choice of OpenAI.
		{"node base URL at the provider default", "https://api.openai.com/v1", withURL, ollama},
		{"node base URL set", "https://gateway.example.test/v1", withURL, "https://gateway.example.test/v1"},
		{"credential with no base URL", nil, openAICredential(), "https://api.openai.com/v1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			parameters := map[string]any{"model": modelLocator("llama3")}
			if test.baseURL != nil {
				parameters["baseUrl"] = test.baseURL
			}
			item := runProviderModel(t, nodes.OpenAIChatModelNodeType, nodes.OpenAIChatModelExecutorID,
				nodes.OpenAICredentialType, parameters, test.resolver)
			descriptor, _ := item.JSON["$ai"].(map[string]any)
			if got := descriptor["baseUrl"]; got != test.want {
				t.Errorf("descriptor baseUrl = %#v, want %q", got, test.want)
			}
		})
	}
}
