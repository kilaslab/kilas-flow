package nodes_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/nodes"
)

// TestAnUnscopedProviderKeyCannotBeRePointedThroughTheModelsBaseURL is the
// exploit the model node's editable base URL used to allow: a provider key saved
// with no allowed domains, and an editor — an embedded guest, an agent token —
// that sets baseUrl to a server they run. The scope check on that call passed
// whenever the list was empty, so the key reached the attacker. The type's
// default scope now answers for an empty list, and the loopback stub here
// stands in for the attacker's server: the policy lets the request out, so the
// credential scope is the only thing that can stop it.
func TestAnUnscopedProviderKeyCannotBeRePointedThroughTheModelsBaseURL(t *testing.T) {
	t.Parallel()
	for _, provider := range []struct {
		name, nodeType, executorID, credentialType string
	}{
		{"openai", nodes.OpenAIChatModelNodeType, nodes.OpenAIChatModelExecutorID, nodes.OpenAICredentialType},
		{"openrouter", nodes.OpenRouterChatModelNodeType, nodes.OpenRouterChatModelExecutorID, nodes.OpenRouterCredentialType},
	} {
		t.Run(provider.name, func(t *testing.T) {
			t.Parallel()
			var received map[string]any
			attacker := answerOnce(&received, 0)
			defer attacker.Close()

			resolver := &stubCredentials{credential: engine.Credential{
				ID: "cred-key", Name: "Provider key", Type: provider.credentialType,
				Fields: map[string]string{"apiKey": "sk-live-secret"},
			}}
			descriptor := runProviderModel(t, provider.nodeType, provider.executorID, provider.credentialType,
				map[string]any{"model": modelLocator("gpt-test"), "baseUrl": attacker.URL, "stream": false},
				resolver)

			executor := nodes.NewAgentExecutor(ai.NewLoopRuntime(), localPolicy(), nil)
			_, err := runAgentWith(t, executor, descriptor, resolver)
			if err == nil || !strings.Contains(err.Error(), "not allowed for host") {
				t.Fatalf("Execute() error = %v, want the type's default scope to refuse the base URL", err)
			}
			if received != nil {
				t.Error("the provider key was sent to a host outside its default scope")
			}
		})
	}
}
