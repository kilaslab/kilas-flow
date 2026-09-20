package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// newTestFlagSet is a FlagSet a test can parse into without a verb.
func newTestFlagSet(t *testing.T) *flag.FlagSet {
	t.Helper()

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(&strings.Builder{})

	return fs
}

// testLoginToken is shaped like a real API key so the server's own validation
// is not what a test is proving.
const testLoginToken = "kfa1_5f3a2b1c9d4e6f708192a3b4c5d6e7f8_unit-test-secret"

// homeEnv is an Env's getter for a caller whose HOME points at dir and whose
// environment holds the extra variables given.
func homeEnv(dir string, extra map[string]string) func(string) string {
	return func(name string) string {
		if name == "HOME" {
			return dir
		}

		return extra[name]
	}
}

// writeTestConfig writes a configuration file the way `auth login` would.
func writeTestConfig(t *testing.T, path, url, token string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body, err := toml.Marshal(fileConfig{URL: url, Token: token})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// defaultPathFor is where a caller with this HOME keeps the configuration file.
func defaultPathFor(home string) string {
	return filepath.Join(home, ".config", "kilasflow", "config.toml")
}

// settingsFrom resolves the chain for one invocation's arguments.
func settingsFrom(t *testing.T, home string, extra map[string]string, args ...string) Settings {
	t.Helper()

	fs := newTestFlagSet(t)
	flags := registerGlobalFlags(fs)
	if _, err := parseFlags(fs, flags, args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}

	settings, err := resolveSettings(Env{Getenv: homeEnv(home, extra)}, flags)
	if err != nil {
		t.Fatalf("resolveSettings(%v): %v", args, err)
	}

	return settings
}

func TestConfigPrecedence(t *testing.T) {
	home := t.TempDir()
	writeTestConfig(t, defaultPathFor(home), "http://default-file.test", "default-file-token")

	other := t.TempDir()
	named := filepath.Join(other, "named.toml")
	writeTestConfig(t, named, "http://named-file.test", "named-file-token")

	envURL := map[string]string{envURLVar: "http://env.test", envTokenVar: "env-token"}
	missing := filepath.Join(home, ".config", "kilasflow", "absent.toml")
	bare := t.TempDir()

	cases := []struct {
		name      string
		home      string
		extra     map[string]string
		args      []string
		wantURL   string
		wantToken string
		wantPath  string
	}{
		{
			name: "the --url and --token flags beat the environment and the file",
			home: home, extra: envURL,
			args:    []string{"--url", "http://flag.test", "--token", "flag-token"},
			wantURL: "http://flag.test", wantToken: "flag-token", wantPath: defaultPathFor(home),
		},
		{
			name: "the environment beats the file",
			home: home, extra: envURL,
			wantURL: "http://env.test", wantToken: "env-token", wantPath: defaultPathFor(home),
		},
		{
			name:    "the file beats the built-in default",
			home:    home,
			wantURL: "http://default-file.test", wantToken: "default-file-token", wantPath: defaultPathFor(home),
		},
		{
			name: "--config replaces the default path rather than merging with it",
			home: home, args: []string{"--config", named},
			wantURL: "http://named-file.test", wantToken: "named-file-token", wantPath: named,
		},
		{
			name: "--config names a file that does not exist, so the chain falls through",
			home: home, args: []string{"--config", missing},
			wantURL: defaultBaseURL, wantToken: "", wantPath: missing,
		},
		{
			name: "--config '' means no configuration file at all",
			home: home, args: []string{"--config", ""},
			wantURL: defaultBaseURL, wantToken: "", wantPath: "",
		},
		{
			name:    "the built-in default when nothing is set",
			home:    bare,
			wantURL: defaultBaseURL, wantToken: "", wantPath: defaultPathFor(bare),
		},
		{
			name:    "an explicitly empty --url means no server",
			home:    bare,
			args:    []string{"--url", ""},
			wantURL: "", wantToken: "", wantPath: defaultPathFor(bare),
		},
		{
			name: "an explicitly empty --token overrides a stored one",
			home: home, args: []string{"--token", ""},
			wantURL: "http://default-file.test", wantToken: "", wantPath: defaultPathFor(home),
		},
		{
			name: "--token-file ranks with --token and beats the environment",
			home: home, extra: envURL,
			args:    []string{"--token-file", fileAt(t, "token-file-token", 0o600)},
			wantURL: "http://env.test", wantToken: "token-file-token", wantPath: defaultPathFor(home),
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := settingsFrom(t, testCase.home, testCase.extra, testCase.args...)
			if got.URL != testCase.wantURL {
				t.Errorf("URL = %q, want %q", got.URL, testCase.wantURL)
			}
			if got.Token != testCase.wantToken {
				t.Errorf("Token = %q, want %q", got.Token, testCase.wantToken)
			}
			if got.ConfigPath != testCase.wantPath {
				t.Errorf("ConfigPath = %q, want %q", got.ConfigPath, testCase.wantPath)
			}
		})
	}
}

// fileAt writes content to a fresh file with the mode given and returns its
// path.
func fileAt(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	// WriteFile is subject to the umask, and a test about a mode has to state
	// the mode it means.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}

	return path
}

func TestConfigFileChainReachesTheRequest(t *testing.T) {
	home := t.TempDir()
	writeTestConfig(t, defaultPathFor(home), "http://file.test", testLoginToken)

	var (
		gotPath  string
		gotToken string
	)
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/health": func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotToken = r.Header.Get("Authorization")
			jsonBody(http.StatusOK, healthBody)(w, r)
		},
	})

	// The environment names the stub, the file names the credential: the two
	// rungs of the chain that a real caller uses are exercised together.
	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"health", "--json"},
		Getenv: homeEnv(home, map[string]string{envURLVar: srv.URL}),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}
	if gotPath != apiPrefix+"/health" {
		t.Fatalf("path = %q, want %q", gotPath, apiPrefix+"/health")
	}
	if gotToken != "Bearer "+testLoginToken {
		t.Fatalf("Authorization = %q, want the file's token", gotToken)
	}
	if strings.Contains(stdout, testLoginToken) {
		t.Fatalf("stdout leaked the token: %q", stdout)
	}
}

func TestMissingURLNamesEveryWayToSetOne(t *testing.T) {
	home := t.TempDir()

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"health", "--url", "", "--json"},
		Getenv: homeEnv(home, nil),
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitUsage, stdout, stderr)
	}

	doc := envelope(t, stdout)
	failure, _ := doc["error"].(map[string]any)
	message, _ := failure["message"].(string)
	for _, source := range []string{"--url", envURLVar, "--config", defaultPathFor(home)} {
		if !strings.Contains(message, source) {
			t.Errorf("the refusal does not name %s: %q", source, message)
		}
	}
}

// A base URL the client cannot dial is the caller's mistake, refused as a
// usage error before any request: a scheme that is not http(s), or a base with
// no host, can never reach the server the verb was pointed at.
func TestAnUndialableServerURLIsAUsageError(t *testing.T) {
	for _, base := range []string{"ftp://example.test", "example.test"} {
		t.Run(base, func(t *testing.T) {
			code, _, stdout, stderr := runCLI(t, Env{
				Args:   []string{"health", "--url", base, "--json"},
				Getenv: homeEnv(t.TempDir(), nil),
				TTY:    true,
			})
			if code != ExitUsage {
				t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitUsage, stdout, stderr)
			}

			doc := envelope(t, stdout)
			failure, _ := doc["error"].(map[string]any)
			if failure["code"] != "usage" {
				t.Fatalf("error.code = %v, want usage", failure["code"])
			}
			message, _ := failure["message"].(string)
			if !strings.Contains(message, base) {
				t.Fatalf("error.message = %q, want it to quote %s", message, base)
			}
		})
	}
}

func TestLoginStoresTheTokenAt0600(t *testing.T) {
	home := t.TempDir()
	me := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/auth/me": jsonBody(http.StatusOK, `{"tenantId":"t_1","kind":"api_key","label":"agent","keyId":"k_1"}`),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"auth", "login", "--url", me.URL, "--token", "-", "--json"},
		Stdin:  strings.NewReader(testLoginToken + "\n"),
		Getenv: homeEnv(home, nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["url"] != me.URL {
		t.Errorf("data.url = %v, want %v", data["url"], me.URL)
	}
	if data["tenantId"] != "t_1" || data["kind"] != "api_key" {
		t.Errorf("data = %v, want the identity the token authenticates as", data)
	}

	path := defaultPathFor(home)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the configuration file was not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %04o, want 0600", perm)
	}

	var stored fileConfig
	if _, err := toml.DecodeFile(path, &stored); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stored.Token != testLoginToken || stored.URL != me.URL {
		t.Fatalf("stored = %+v, want the token and the URL", stored)
	}

	// The round trip: the next invocation reads the credential it just stored.
	if _, err := toml.DecodeFile(path, &stored); err != nil || stored.Token == "" {
		t.Fatalf("the stored token does not read back: %+v (%v)", stored, err)
	}

	// The token is never echoed, in either stream.
	if strings.Contains(stdout, testLoginToken) || strings.Contains(stderr, testLoginToken) {
		t.Fatalf("login printed the token (stdout=%q stderr=%q)", stdout, stderr)
	}
}

func TestLoginTightensAnExistingFilesMode(t *testing.T) {
	home := t.TempDir()
	path := defaultPathFor(home)
	writeTestConfig(t, path, "http://old.test", "old-token")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	me := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/auth/me": jsonBody(http.StatusOK, `{"tenantId":"t_1","kind":"api_key"}`),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"auth", "login", "--url", me.URL, "--token", "-", "--json"},
		Stdin:  strings.NewReader(testLoginToken),
		Getenv: homeEnv(home, nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %04o, want the existing 0644 file tightened to 0600", perm)
	}
}

func TestLoginRefusesATokenOnArgv(t *testing.T) {
	// The request count comes from a transport the test owns: a refusal that
	// reached the server would already have leaked the token into a log.
	requests := &countingTransport{document: "{}"}
	client := &Client{BaseURL: "http://example.test", HTTP: &http.Client{Transport: requests}}

	code, _, stderr := driveVerb(t, verbByPath(t, "auth login"), client,
		"auth", "login", "--url", "http://example.test", "--token", testLoginToken)
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if requests.requests.Load() != 0 {
		t.Fatalf("the refusal sent %d requests, want none", requests.requests.Load())
	}
	if !strings.Contains(stderr, "--token -") {
		t.Fatalf("the refusal does not name the ways to pass a token: %q", stderr)
	}

	// The file half goes through Run, which is the only path that resolves a
	// configuration path from HOME.
	home := t.TempDir()
	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"auth", "login", "--url", "http://127.0.0.1:1", "--token", testLoginToken, "--json"},
		Getenv: homeEnv(home, nil),
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitUsage, stdout, stderr)
	}
	if _, err := os.Stat(defaultPathFor(home)); !os.IsNotExist(err) {
		t.Fatalf("a refused login wrote a configuration file: %v", err)
	}
	if strings.Contains(stdout, testLoginToken) || strings.Contains(stderr, testLoginToken) {
		t.Fatalf("the refusal echoed the token (stdout=%q stderr=%q)", stdout, stderr)
	}
}

func TestLoginDoesNotStoreATokenTheServerRefuses(t *testing.T) {
	home := t.TempDir()
	me := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/auth/me": problemBody(http.StatusUnauthorized, `{"title":"Unauthorized","status":401,"detail":"This request needs an API key or a signed-in session."}`),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"auth", "login", "--url", me.URL, "--token", "-", "--json"},
		Stdin:  strings.NewReader("kfa1_bad_nope"),
		Getenv: homeEnv(home, nil),
	})
	if code != ExitRefused {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitRefused, stdout, stderr)
	}
	if _, err := os.Stat(defaultPathFor(home)); !os.IsNotExist(err) {
		t.Fatalf("an invalid token was stored: %v", err)
	}
}

func TestTokenFileRefusedWhenGroupOrWorldReadable(t *testing.T) {
	readable := fileAt(t, testLoginToken, 0o644)
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/health": jsonBody(http.StatusOK, healthBody),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"health", "--url", srv.URL, "--token-file", readable, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitUsage, stdout, stderr)
	}

	doc := envelope(t, stdout)
	failure, _ := doc["error"].(map[string]any)
	message, _ := failure["message"].(string)
	if !strings.Contains(message, "chmod 600") {
		t.Fatalf("the refusal does not say how to fix it: %q", message)
	}

	private := fileAt(t, testLoginToken, 0o600)
	var gotToken string
	privateSrv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/health": func(w http.ResponseWriter, r *http.Request) {
			gotToken = r.Header.Get("Authorization")
			jsonBody(http.StatusOK, healthBody)(w, r)
		},
	})

	code, _, stdout, stderr = runCLI(t, Env{
		Args:   []string{"health", "--url", privateSrv.URL, "--token-file", private, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}
	if gotToken != "Bearer "+testLoginToken {
		t.Fatalf("Authorization = %q, want the token-file's token", gotToken)
	}
}

func TestTokenIsNeverPrinted(t *testing.T) {
	home := t.TempDir()
	writeTestConfig(t, defaultPathFor(home), "http://file.test", testLoginToken)

	// A problem document that echoes the credential is the worst case: the
	// server does not do it, and a proxy or a future handler might.
	echoing := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/auth/me": problemBody(http.StatusUnauthorized,
			`{"title":"Unauthorized","status":401,"detail":"the key `+testLoginToken+` is revoked","errors":[{"message":"`+testLoginToken+`"}]}`),
		apiPrefix + "/health":     jsonBody(http.StatusOK, healthBody),
		apiPrefix + "/ready":      jsonBody(http.StatusOK, `{"status":"ready"}`),
		apiPrefix + "/workflows":  jsonBody(http.StatusOK, `[]`),
		apiPrefix + "/datastores": jsonBody(http.StatusOK, `{"items":[]}`),
		apiPrefix + "/node-types": jsonBody(http.StatusOK, `[]`),
		"/api/openapi.json": jsonBody(http.StatusOK,
			`{"paths":{"/health":{"get":{"operationId":"get-health"}}}}`),
	})

	cases := []struct {
		name string
		args []string
	}{
		{name: "auth whoami", args: []string{"auth", "whoami", "--json"}},
		{name: "context", args: []string{"context", "--json"}},
		{name: "api --list", args: []string{"api", "--list", "--json"}},
		{name: "health --verbose", args: []string{"health", "--verbose", "--json"}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			env := map[string]string{envURLVar: echoing.URL}

			code, _, stdout, stderr := runCLI(t, Env{Args: testCase.args, Getenv: homeEnv(home, env)})
			if code != ExitOK && code != ExitRefused {
				t.Fatalf("exit = %d (stdout=%q stderr=%q)", code, stdout, stderr)
			}
			if strings.Contains(stdout, testLoginToken) {
				t.Fatalf("stdout leaked the token: %q", stdout)
			}
			if strings.Contains(stderr, testLoginToken) {
				t.Fatalf("stderr leaked the token: %q", stderr)
			}
		})
	}
}

func TestLogoutRemovesTheTokenAndKeepsTheURL(t *testing.T) {
	home := t.TempDir()
	path := defaultPathFor(home)
	writeTestConfig(t, path, "http://file.test", testLoginToken)

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"auth", "logout", "--json"},
		Getenv: homeEnv(home, nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	var stored fileConfig
	if _, err := toml.DecodeFile(path, &stored); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stored.Token != "" {
		t.Fatalf("the token survived logout: %+v", stored)
	}
	if stored.URL != "http://file.test" {
		t.Fatalf("logout dropped the URL: %+v", stored)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["url"] != "http://file.test" || data["configPath"] != path {
		t.Fatalf("data = %v, want the url and the config path", data)
	}

	// With no file at all it is a no-op that still succeeds.
	empty := t.TempDir()
	code, _, stdout, stderr = runCLI(t, Env{
		Args:   []string{"auth", "logout", "--json"},
		Getenv: homeEnv(empty, nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}
	if _, err := os.Stat(defaultPathFor(empty)); !os.IsNotExist(err) {
		t.Fatalf("logout created a configuration file: %v", err)
	}
}

func TestAuthWhoamiProjectsTheIdentityAndNeverTheCredential(t *testing.T) {
	home := t.TempDir()
	writeTestConfig(t, defaultPathFor(home), "http://file.test", testLoginToken)

	me := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/auth/me": jsonBody(http.StatusOK,
			`{"tenantId":"t_1","kind":"api_key","label":"agent","keyId":"k_1","userId":"u_1","token":"`+testLoginToken+`"}`),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"auth", "whoami", "--json"},
		Getenv: homeEnv(home, map[string]string{envURLVar: me.URL}),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	for field, want := range map[string]string{"tenantId": "t_1", "kind": "api_key", "label": "agent", "keyId": "k_1", "userId": "u_1"} {
		if data[field] != want {
			t.Errorf("data.%s = %v, want %q", field, data[field], want)
		}
	}
	if _, present := data["token"]; present {
		t.Fatalf("whoami carried a token field: %v", data)
	}
	if strings.Contains(stdout, testLoginToken) {
		t.Fatalf("stdout leaked the token: %q", stdout)
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "get-me" {
		t.Fatalf("meta.operation = %v, want get-me", meta["operation"])
	}
}

func TestSaveConfigEscapesATokenThatNeedsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.toml")
	awkward := "kfa1_5f3a2b1c9d4e6f708192a3b4c5d6e7f8_a\"b\nc\\d"

	if err := saveConfig(path, fileConfig{URL: "http://x.test", Token: awkward}); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	var stored fileConfig
	if _, err := toml.DecodeFile(path, &stored); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stored.Token != awkward {
		t.Fatalf("token round trip = %q, want %q", stored.Token, awkward)
	}

	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("config directory mode = %04o, want 0700", perm)
	}
}

func TestAuthLoginRefusesWithoutATokenSource(t *testing.T) {
	home := t.TempDir()
	me := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/auth/me": jsonBody(http.StatusOK, `{"tenantId":"t_1"}`),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"auth", "login", "--url", me.URL, "--json"},
		Getenv: homeEnv(home, nil),
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitUsage, stdout, stderr)
	}

	doc := envelope(t, stdout)
	failure, _ := doc["error"].(map[string]any)
	message, _ := failure["message"].(string)
	if !strings.Contains(message, envTokenVar) {
		t.Fatalf("the refusal does not name %s: %q", envTokenVar, message)
	}
}

func TestResolveSettingsRejectsATokenAndATokenFileTogether(t *testing.T) {
	fs := newTestFlagSet(t)
	flags := registerGlobalFlags(fs)
	if _, err := parseFlags(fs, flags, []string{"--token", "a", "--token-file", "b"}); err != nil {
		t.Fatalf("parse: %v", err)
	}

	_, err := resolveSettings(Env{Getenv: homeEnv(t.TempDir(), nil)}, flags)
	if err == nil {
		t.Fatal("resolveSettings accepted --token and --token-file together")
	}
	var failure *ExitError
	if !errors.As(err, &failure) || failure.Code != ExitUsage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

// TestSettingsAreResolvedOncePerInvocation pins the caching rule: one
// invocation reads the file once, so a verb and the client cannot disagree
// about what the configuration said.
func TestSettingsAreResolvedOncePerInvocation(t *testing.T) {
	home := t.TempDir()
	path := defaultPathFor(home)
	writeTestConfig(t, path, "http://file.test", testLoginToken)

	env := Env{Getenv: homeEnv(home, nil)}
	ctx := &Context{Env: env, Flags: &GlobalFlags{}}
	first, err := ctx.settings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}

	// The file changes underneath, which a second read would notice.
	writeTestConfig(t, path, "http://changed.test", "changed-token")
	second, err := ctx.settings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if first != second {
		t.Fatalf("settings = %+v then %+v, want one resolution per invocation", first, second)
	}
}

// TestConfigFileMustBeTOMLAndIsARefusalNotACrash covers a hand-edited file.
func TestConfigFileMustBeTOMLAndIsARefusalNotACrash(t *testing.T) {
	home := t.TempDir()
	path := defaultPathFor(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("url = \"http://x.test\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"health", "--json"},
		Getenv: homeEnv(home, nil),
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitUsage, stdout, stderr)
	}
	if !strings.Contains(stdout, "TOML") {
		t.Fatalf("the refusal does not say what is wrong: %q", stdout)
	}
}

// TestVerboseTraceNeverCarriesTheTokenInABody is the body half of the
// redaction rule: a request body is traced, and it must be redacted first.
func TestVerboseTraceNeverCarriesTheTokenInABody(t *testing.T) {
	home := t.TempDir()

	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/workflows": jsonBody(http.StatusCreated, `{"id":"wf_1"}`),
	})

	file := fileAt(t, testLoginToken, 0o600)
	body := filepath.Join(t.TempDir(), "doc.json")
	if err := os.WriteFile(body, []byte(`{"schemaVersion":1,"name":"x","nodes":[],"connections":[],"settings":{}}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "create", "--file", body, "--url", srv.URL, "--token-file", file, "--verbose", "--json"},
		Getenv: homeEnv(home, nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}
	if strings.Contains(stderr, testLoginToken) || strings.Contains(stdout, testLoginToken) {
		t.Fatalf("the trace leaked the token (stdout=%q stderr=%q)", stdout, stderr)
	}
	if !strings.Contains(stderr, "> POST ") {
		t.Fatalf("the trace is missing the request line: %q", stderr)
	}
}

// TestProblemDocumentsAreRedactedBeforeTheyAreCarried pins the one place a
// credential could reach the envelope: the server's own error document.
func TestProblemDocumentsAreRedactedBeforeTheyAreCarried(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/auth/me": problemBody(http.StatusUnauthorized,
			`{"title":"Unauthorized","status":401,"detail":"key `+testLoginToken+` is revoked"}`),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"auth", "whoami", "--url", srv.URL, "--token", testLoginToken, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitRefused {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitRefused, stdout, stderr)
	}

	doc := envelope(t, stdout)
	failure, _ := doc["error"].(map[string]any)
	detail, _ := failure["detail"].(map[string]any)
	problem, _ := json.Marshal(detail["problem"])
	if strings.Contains(string(problem), testLoginToken) {
		t.Fatalf("the problem document carried the token: %s", problem)
	}
	if !strings.Contains(string(problem), "[redacted]") {
		t.Fatalf("the problem document was not redacted: %s", problem)
	}
}
