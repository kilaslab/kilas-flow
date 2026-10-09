package credentials_test

import (
	"bytes"
	"context"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
)

func TestParseKeyReportsARawString(t *testing.T) {
	key := bytes.Repeat([]byte{0xab}, credentials.KeySize)
	raw := "abcdefghijklmnopqrstuvwxyz012345"
	for name, test := range map[string]struct {
		input  string
		want   []byte
		format credentials.KeyFormat
	}{
		"base64": {base64.StdEncoding.EncodeToString(key), key, credentials.KeyFormatBase64},
		"hex":    {hex.EncodeToString(key), key, credentials.KeyFormatHex},
		"raw":    {raw, []byte(raw), credentials.KeyFormatRaw},
	} {
		got, format, err := credentials.ParseKey(test.input)
		if err != nil {
			t.Errorf("%s: ParseKey() error = %v", name, err)
			continue
		}
		if !bytes.Equal(got, test.want) || format != test.format {
			t.Errorf("%s: ParseKey() = %x, %v, want %x, %v", name, got, format, test.want, test.format)
		}
	}
	for _, bad := range []string{"", "short", base64.StdEncoding.EncodeToString(key[:16])} {
		if _, _, err := credentials.ParseKey(bad); err == nil {
			t.Errorf("ParseKey(%q) accepted a key that is not 32 bytes", bad)
		}
	}

	t.Run("the environment path reports the format", func(t *testing.T) {
		t.Setenv("KILASFLOW_TEST_KEYSOURCE_RAW", raw)
		got, format, err := credentials.KeyFromEnvironmentFormat("KILASFLOW_TEST_KEYSOURCE_RAW")
		if err != nil || format != credentials.KeyFormatRaw || string(got) != raw {
			t.Fatalf("KeyFromEnvironmentFormat() = %q, %v, %v", got, format, err)
		}
		// The long-standing entry point still accepts it unchanged.
		if got, err := credentials.KeyFromEnvironment("KILASFLOW_TEST_KEYSOURCE_RAW"); err != nil || string(got) != raw {
			t.Fatalf("KeyFromEnvironment() = %q, %v", got, err)
		}
	})

	t.Run("the manager path reports the format", func(t *testing.T) {
		provider := fixedProvider{value: raw}
		got, format, err := credentials.KeyFromManagerFormat(context.Background(), provider, "prod/master")
		if err != nil || format != credentials.KeyFormatRaw || string(got) != raw {
			t.Fatalf("KeyFromManagerFormat() = %q, %v, %v", got, format, err)
		}
	})
}

type fixedProvider struct{ value string }

func (fixedProvider) Name() string                 { return "fixed" }
func (fixedProvider) Health(context.Context) error { return nil }
func (provider fixedProvider) Fetch(context.Context, string) (string, error) {
	return provider.value, nil
}

func TestDeriveKeySeparatesPurposes(t *testing.T) {
	t.Parallel()

	master := bytes.Repeat([]byte{0x5a}, credentials.KeySize)
	oauth, err := credentials.DeriveKey(master, credentials.OAuthStateKeyInfo)
	if err != nil {
		t.Fatalf("DeriveKey(oauth) error = %v", err)
	}
	other, err := credentials.DeriveKey(master, "kilasflow/something-else/v1")
	if err != nil {
		t.Fatalf("DeriveKey(other) error = %v", err)
	}
	if len(oauth) != credentials.KeySize || bytes.Equal(oauth, other) || bytes.Equal(oauth, master) {
		t.Fatalf("DeriveKey() did not separate purposes: oauth=%x other=%x", oauth, other)
	}
	want, err := hkdf.Key(sha256.New, master, nil, "kilasflow/oauth-state/v1", credentials.KeySize)
	if err != nil {
		t.Fatalf("hkdf.Key() error = %v", err)
	}
	if !bytes.Equal(oauth, want) {
		t.Fatal("the OAuth key is not HKDF-SHA256(master, info \"kilasflow/oauth-state/v1\")")
	}
	again, _ := credentials.DeriveKey(master, credentials.OAuthStateKeyInfo)
	if !bytes.Equal(oauth, again) {
		t.Fatal("DeriveKey() is not deterministic, so a replica could not verify another's state")
	}
	if _, err := credentials.DeriveKey(master[:16], credentials.OAuthStateKeyInfo); err == nil {
		t.Error("DeriveKey() accepted a 16-byte master key")
	}
	if _, err := credentials.DeriveKey(master, ""); err == nil {
		t.Error("DeriveKey() accepted an empty purpose")
	}
}
