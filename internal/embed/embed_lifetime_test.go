package embed_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/embed"
)

// lifetimeIssuer builds an issuer on a fixed clock with the given options, so a
// test can read a session's lifetime as an exact duration.
func lifetimeIssuer(t *testing.T, options ...embed.IssuerOption) *embed.Issuer {
	t.Helper()
	fixed := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	issuer, err := embed.NewIssuer(testKey(), []string{"https://host.example"},
		func() time.Time { return fixed }, options...)
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}
	return issuer
}

func TestConfiguredDefaultLifetimeAppliesWhenTheCallerAsksForNone(t *testing.T) {
	t.Parallel()

	issuer := lifetimeIssuer(t, embed.WithDefaultLifetime(5*time.Minute))
	session, token, err := issuer.Issue(validRequest())
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if got := session.ExpiresAt.Sub(session.IssuedAt); got != 5*time.Minute {
		t.Fatalf("lifetime = %s, want the configured 5m default", got)
	}

	verified, err := issuer.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if got := verified.ExpiresAt.Sub(verified.IssuedAt); got != 5*time.Minute {
		t.Errorf("verified lifetime = %s, want 5m: the token itself must carry the configured default", got)
	}
}

func TestACallersLifetimeStillOverridesTheConfiguredDefault(t *testing.T) {
	t.Parallel()

	issuer := lifetimeIssuer(t, embed.WithDefaultLifetime(5*time.Minute))
	for _, asked := range []time.Duration{20 * time.Minute, 2 * time.Minute} {
		request := validRequest()
		request.Lifetime = asked
		session, _, err := issuer.Issue(request)
		if err != nil {
			t.Fatalf("Issue(%s) error = %v", asked, err)
		}
		// The setting is a default, not a ceiling: a host that names a lifetime
		// gets it, longer or shorter, up to the hard cap.
		if got := session.ExpiresAt.Sub(session.IssuedAt); got != asked {
			t.Errorf("Issue(%s) lifetime = %s, want the caller's", asked, got)
		}
	}
}

func TestTheHardCapHoldsWhateverTheDefaultIs(t *testing.T) {
	t.Parallel()

	issuer := lifetimeIssuer(t, embed.WithDefaultLifetime(embed.MaxLifetime))

	request := validRequest()
	request.Lifetime = 24 * time.Hour
	session, _, err := issuer.Issue(request)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if got := session.ExpiresAt.Sub(session.IssuedAt); got != embed.MaxLifetime {
		t.Errorf("a 24h request lifetime = %s, want the %s hard cap", got, embed.MaxLifetime)
	}

	session, _, err = issuer.Issue(validRequest())
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if got := session.ExpiresAt.Sub(session.IssuedAt); got != embed.MaxLifetime {
		t.Errorf("a default of the cap = %s, want %s", got, embed.MaxLifetime)
	}
}

func TestDefaultLifetimeIsRefusedWhenItCouldNeverBeHonoured(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lifetime time.Duration
		refused  bool
	}{
		{"zero", 0, true},
		{"negative", -time.Second, true},
		// `session_ttl: 900` in YAML decodes as 900 nanoseconds with no error.
		{"a bare YAML number is nanoseconds", 900 * time.Nanosecond, true},
		{"just under the floor", 999 * time.Millisecond, true},
		{"just over the cap", embed.MaxLifetime + time.Nanosecond, true},
		{"the floor", time.Second, false},
		{"a minute", time.Minute, false},
		{"the cap", embed.MaxLifetime, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			checkErr := embed.CheckDefaultLifetime(test.lifetime)
			_, newErr := embed.NewIssuer(testKey(), nil, nil, embed.WithDefaultLifetime(test.lifetime))

			// One range, two entry points: the configuration validator and the
			// issuer option must never disagree about it.
			if (checkErr != nil) != test.refused {
				t.Fatalf("CheckDefaultLifetime(%s) error = %v, want refused=%t", test.lifetime, checkErr, test.refused)
			}
			if (newErr != nil) != test.refused {
				t.Fatalf("NewIssuer(WithDefaultLifetime(%s)) error = %v, want refused=%t", test.lifetime, newErr, test.refused)
			}
			if !test.refused {
				return
			}
			for name, err := range map[string]error{"CheckDefaultLifetime": checkErr, "NewIssuer": newErr} {
				message := err.Error()
				for _, want := range []string{test.lifetime.String(), embed.MinDefaultLifetime.String(), embed.MaxLifetime.String()} {
					if !strings.Contains(message, want) {
						t.Errorf("%s error %q does not name %q: an operator must see the value and both bounds", name, message, want)
					}
				}
			}
		})
	}
}

func TestAnIssuerWithNoOptionKeepsTheBuiltInDefault(t *testing.T) {
	t.Parallel()

	session, _, err := lifetimeIssuer(t).Issue(validRequest())
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if got := session.ExpiresAt.Sub(session.IssuedAt); got != embed.DefaultLifetime {
		t.Errorf("lifetime = %s, want the built-in %s", got, embed.DefaultLifetime)
	}
}
