package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// File modes for the CLI's own configuration. The file holds a credential, so
// it is readable by its owner alone and so is the directory above it.
const (
	configDirPerm  fs.FileMode = 0o700
	configFilePerm fs.FileMode = 0o600
)

// configHeader is written above the document. A TOML encoder cannot emit a
// comment, and a caller who finds this file by accident deserves to know what
// wrote it and what will overwrite it.
const configHeader = "# Written by `kilasflow auth login`. `kilasflow auth logout` removes the token.\n"

// fileConfig is the configuration file's document.
//
// Both keys are optional: a file that names only the server is legitimate, and
// `auth logout` leaves exactly that behind.
type fileConfig struct {
	URL   string `toml:"url,omitempty"`
	Token string `toml:"token,omitempty"`
}

// Settings is the resolved configuration chain for one invocation.
//
// It is the answer to "which server, with which credential", and it also
// carries where that answer came from, because the credential verbs have to
// write back to the same file they read.
type Settings struct {
	// URL is the server to talk to, or "" when the caller explicitly asked for
	// no server (`--url ''`).
	URL string
	// Token is the credential to present, or "" for an unauthenticated
	// request.
	Token string
	// ConfigPath is the file this invocation read, whether or not it existed.
	// It is empty when the caller passed `--config ''`.
	ConfigPath string
	// File is the configuration file as it was read, and FileExists says
	// whether there was one. A verb that rewrites the file keeps what it does
	// not own, so it needs both.
	File       fileConfig
	FileExists bool
}

// defaultConfigPath is where a caller's own settings live when they name no
// file.
//
// It is deliberately not os.UserConfigDir: on darwin that answers
// ~/Library/Application Support, and the design names ~/.config on every
// platform so an agent's instructions do not depend on the operating system.
func defaultConfigPath(env Env) string {
	home := strings.TrimSpace(getenv(env, "HOME"))
	if home == "" {
		return ""
	}

	return filepath.Join(home, ".config", "kilasflow", "config.toml")
}

// resolveSettings applies the configuration chain, highest precedence first:
//
//  1. the --url and --token flags
//  2. KILASFLOW_URL and KILASFLOW_TOKEN
//  3. the file named by --config, which replaces the default path rather than
//     merging with it
//  4. ~/.config/kilasflow/config.toml
//
// The built-in default address is the last resort for the URL. A caller who
// passes an empty --url is not asking for that default: they are saying "no
// server", which is how `version` reports on the binary alone.
func resolveSettings(env Env, flags *GlobalFlags) (Settings, error) {
	settings := Settings{ConfigPath: defaultConfigPath(env)}
	if flags.wasProvided("config") {
		settings.ConfigPath = strings.TrimSpace(flags.Config)
	}

	file, exists, err := readConfigFile(settings.ConfigPath)
	if err != nil {
		return Settings{}, err
	}
	settings.File, settings.FileExists = file, exists

	switch {
	case flags.wasProvided("url"):
		settings.URL = strings.TrimSpace(flags.URL)
	case strings.TrimSpace(getenv(env, envURLVar)) != "":
		settings.URL = strings.TrimSpace(getenv(env, envURLVar))
	case file.URL != "":
		settings.URL = file.URL
	default:
		settings.URL = defaultBaseURL
	}

	token, err := resolveToken(env, flags, file)
	if err != nil {
		return Settings{}, err
	}
	settings.Token = token

	return settings, nil
}

// resolveToken applies the same chain to the credential.
//
// --token and --token-file are one rung of it, not two: a flag beats an
// environment variable, and a caller who passes both is refusing to say which
// credential they mean.
func resolveToken(env Env, flags *GlobalFlags, file fileConfig) (string, error) {
	if flags.wasProvided("token") && flags.wasProvided("token-file") {
		return "", usageError("--token and --token-file are mutually exclusive: pass the credential one way")
	}

	switch {
	case flags.wasProvided("token"):
		return strings.TrimSpace(flags.Token), nil
	case flags.wasProvided("token-file"):
		return readTokenFile(flags.TokenFile)
	case strings.TrimSpace(getenv(env, envTokenVar)) != "":
		return strings.TrimSpace(getenv(env, envTokenVar)), nil
	default:
		return file.Token, nil
	}
}

// readConfigFile reads the configuration file if there is one.
//
// A missing file is not an error: it is the lowest rung of the chain, and an
// installation that has never run `auth login` has none. A file that exists and
// cannot be read or parsed is a usage error, because the caller has to fix it
// and no request should be attempted with half a configuration.
func readConfigFile(path string) (fileConfig, bool, error) {
	if path == "" {
		return fileConfig{}, false, nil
	}

	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fileConfig{}, false, nil
	case err != nil:
		return fileConfig{}, false, usageError("could not read the configuration file %s: %v", path, err)
	}

	var file fileConfig
	if _, err := toml.Decode(string(raw), &file); err != nil {
		return fileConfig{}, false, usageError("%s is not valid TOML: %v", path, err)
	}
	file.URL = strings.TrimSpace(file.URL)
	file.Token = strings.TrimSpace(file.Token)

	return file, true, nil
}

// readTokenFile reads a credential from a file.
//
// The permission check is the point of the flag: a token file that other users
// can read is a token they have, and a CLI that accepted one would be teaching
// its caller that the mode does not matter.
func readTokenFile(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", usageError("--token-file needs a path")
	}

	info, err := os.Stat(trimmed)
	if err != nil {
		return "", usageError("could not read the token file %s: %v", trimmed, err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return "", usageError("%s is readable by other users (mode %04o); chmod 600 it", trimmed, perm)
	}

	raw, err := os.ReadFile(trimmed)
	if err != nil {
		return "", usageError("could not read the token file %s: %v", trimmed, err)
	}

	// A file written by `printf '%s\n' "$TOKEN"` ends in a newline that is not
	// part of the credential.
	token := strings.TrimRight(string(raw), "\r\n")
	if strings.TrimSpace(token) == "" {
		return "", usageError("%s holds no token", trimmed)
	}

	return token, nil
}

// saveConfig writes the configuration file, creating its directory.
//
// The mode is set twice on purpose. os.WriteFile applies its mode only when it
// creates the file, so a configuration an earlier tool left world-readable
// would stay that way — and this file holds a credential. MkdirAll has the same
// problem for the directory, so an existing one is tightened too.
func saveConfig(path string, file fileConfig) error {
	if strings.TrimSpace(path) == "" {
		return usageError("no configuration file to write: pass --config <path> or set HOME")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, configDirPerm); err != nil {
		return configWriteError("could not create %s: %v", dir, err)
	}
	if err := os.Chmod(dir, configDirPerm); err != nil {
		return configWriteError("could not restrict %s: %v", dir, err)
	}

	var body bytes.Buffer
	body.WriteString(configHeader)
	if err := toml.NewEncoder(&body).Encode(file); err != nil {
		return configWriteError("could not render the configuration: %v", err)
	}

	if err := os.WriteFile(path, body.Bytes(), configFilePerm); err != nil {
		return configWriteError("could not write %s: %v", path, err)
	}
	if err := os.Chmod(path, configFilePerm); err != nil {
		return configWriteError("could not restrict %s: %v", path, err)
	}

	return nil
}

// configWriteError is a local failure to store the configuration: the
// invocation was right and the environment would not take it, which is exit 1
// rather than a usage error.
func configWriteError(format string, args ...any) *ExitError {
	return &ExitError{Code: ExitFailure, ErrCode: "config_error", Message: fmt.Sprintf(format, args...)}
}

// settings resolves the chain once per invocation and remembers the answer, so
// the client a verb talks through and the file the credential verbs write
// cannot disagree about what the configuration said.
func (ctx *Context) settings() (Settings, error) {
	if ctx.resolved != nil {
		return *ctx.resolved, nil
	}

	settings, err := resolveSettings(ctx.Env, ctx.Flags)
	if err != nil {
		return Settings{}, err
	}
	ctx.resolved = &settings

	return settings, nil
}
