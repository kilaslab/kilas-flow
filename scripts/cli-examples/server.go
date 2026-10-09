package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// operatorKey is the credential the examples run with. It is registered at boot
// from the environment, so no request is needed to obtain one; the shape
// (kfa1_<hex prefix>_<secret>) is what repository.EnsureAPIKey insists on.
const operatorKey = "kfa1_5f3a2b1c9d4e6f708192a3b4c5d6e7f8_cli-examples-secret"

// bootTimeout is how long a server has to answer /api/v1/ready.
const bootTimeout = 90 * time.Second

// server is one booted instance of the binary under test.
type server struct {
	cmd     *exec.Cmd
	url     string
	logPath string
	exited  chan struct{}
}

// startServer boots the binary against a fresh SQLite database in the work
// directory, with authentication on: half of what the page shows (whoami, a 401
// that has to become exit 3, a scoped refusal) has no meaning on an instance that
// has none. The workflow the examples run calls this server's own health
// endpoint, so the one address it needs is admitted by exact host and port; the
// private-network switch stays off.
func startServer(binary, work string) (*server, error) {
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	signingKey, err := randomKey()
	if err != nil {
		return nil, err
	}
	encryptionKey, err := randomKey()
	if err != nil {
		return nil, err
	}

	// The server's working directory is empty: a directory of packs beside it
	// would add nodes the default install does not have, and the catalogue check
	// below holds the served catalogue to the default one.
	cwd := filepath.Join(work, "server")
	data := filepath.Join(work, "data")
	for _, dir := range []string{cwd, data} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	logPath := filepath.Join(work, "server.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}

	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	command := exec.Command(binary, "serve", "-config", "")
	command.Dir = cwd
	command.Stdout, command.Stderr = logFile, logFile
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + cwd,
		"KILASFLOW_AUTH_ENABLED=true",
		"KILASFLOW_AUTH_SIGNING_KEY=" + signingKey,
		"KILASFLOW_AUTH_OPERATOR_KEY=" + operatorKey,
		"KILASFLOW_ENCRYPTION_KEY=" + encryptionKey,
		"KILASFLOW_SERVER_HOST=127.0.0.1",
		"KILASFLOW_SERVER_PORT=" + strconv.Itoa(port),
		"KILASFLOW_DATABASE_DSN=" + filepath.Join(data, "kilasflow.db"),
		"KILASFLOW_OUTBOUND_ALLOWED_HOSTS=127.0.0.1",
		"KILASFLOW_OUTBOUND_ALLOWED_PRIVATE_ENDPOINTS=" + address,
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", binary, err)
	}

	started := &server{cmd: command, url: "http://" + address, logPath: logPath, exited: make(chan struct{})}
	go func() {
		_ = command.Wait()
		close(started.exited)
	}()

	if err := started.waitReady(); err != nil {
		started.stop()

		return nil, err
	}

	return started, nil
}

// waitReady polls /api/v1/ready until it answers, the process dies, or the
// deadline passes. A server that died is reported with its own log.
func (s *server) waitReady() error {
	ctx, cancel := context.WithTimeout(context.Background(), bootTimeout)
	defer cancel()

	client := &http.Client{Timeout: 2 * time.Second}
	for {
		select {
		case <-s.exited:
			return fmt.Errorf("the server exited while booting:\n%s", s.readLog())
		case <-ctx.Done():
			return fmt.Errorf("the server was not ready after %s:\n%s", bootTimeout, s.readLog())
		case <-time.After(200 * time.Millisecond):
		}

		request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url+"/api/v1/ready", nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			continue
		}
		response.Body.Close()
		if response.StatusCode == http.StatusOK {
			return nil
		}
	}
}

func (s *server) readLog() string {
	raw, err := os.ReadFile(s.logPath)
	if err != nil {
		return "(no log: " + err.Error() + ")"
	}

	return string(raw)
}

// stop ends the server and waits for it, killing it if it ignores the request.
func (s *server) stop() {
	select {
	case <-s.exited:
		return
	default:
	}
	_ = s.cmd.Process.Signal(os.Interrupt)
	select {
	case <-s.exited:
	case <-time.After(10 * time.Second):
		_ = s.cmd.Process.Kill()
		<-s.exited
	}
}

// freePort asks the kernel for an unused loopback port.
func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()

	return listener.Addr().(*net.TCPAddr).Port, nil
}

// randomKey is 32 random bytes, base64-encoded, the form both the session
// signing key and the credential encryption key take.
func randomKey() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(raw), nil
}
