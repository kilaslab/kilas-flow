package config

import "testing"

// The secrets section is all-or-nothing: a half-configured manager would fail
// every fetch at run time, or silently keep the environment path while the
// operator believes the manager is in charge.
func TestSecretsSectionRejectsPartialConfiguration(t *testing.T) {
	empty := Default()
	if err := empty.Validate(); err != nil {
		t.Fatalf("empty secrets Validate() error = %v", err)
	}
	for name, mutate := range map[string]func(*Config){
		"address only": func(c *Config) { c.Secrets.ManagerAddr = "https://vault.internal:8200" },
		"token only":   func(c *Config) { c.Secrets.ManagerTokenEnv = "VAULT_TOKEN" },
		"key only":     func(c *Config) { c.Secrets.MasterKey = "prod/master-key" },
		"no key": func(c *Config) {
			c.Secrets.ManagerAddr = "https://vault.internal:8200"
			c.Secrets.ManagerTokenEnv = "VAULT_TOKEN"
		},
	} {
		cfg := Default()
		mutate(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil, want a partial-configuration refusal", name)
		}
	}
	full := Default()
	full.Secrets = Secrets{
		ManagerAddr: "https://vault.internal:8200", ManagerTokenEnv: "VAULT_TOKEN", MasterKey: "prod/master-key",
	}
	if err := full.Validate(); err != nil {
		t.Errorf("full secrets Validate() error = %v", err)
	}
}
