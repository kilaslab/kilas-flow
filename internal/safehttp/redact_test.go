package safehttp_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

// closedEndpoint is a loopback address nothing listens on, so a request to it
// fails with a transport error rather than an answer.
func closedEndpoint(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

// A transport error names the URL it failed on, path and query included, and
// both are places a credential lives: httpQueryAuth puts its secret in the
// query, and the Telegram Bot API puts a bot token in the path. The redacted
// error keeps the scheme and host an operator needs and nothing after them.
func TestRedactErrorWithholdsThePathAndQueryOfATransportError(t *testing.T) {
	t.Parallel()

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	endpoint := closedEndpoint(t)
	target := "http://" + endpoint + "/bot123456:PATH-SECRET/sendMessage?api_key=QUERY-SECRET"
	request, err := http.NewRequest(http.MethodPost, target, nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	_, raw := safehttp.NewClient(policy).Do(request)
	if raw == nil {
		t.Fatal("a request to a closed port succeeded")
	}
	if !strings.Contains(raw.Error(), "QUERY-SECRET") {
		t.Fatalf("the unredacted error %q does not carry the URL, so this test proves nothing", raw)
	}

	redacted := safehttp.RedactError(raw)
	for _, secret := range []string{"PATH-SECRET", "QUERY-SECRET", "sendMessage", "api_key"} {
		if strings.Contains(redacted.Error(), secret) {
			t.Errorf("redacted error %q still carries %q", redacted, secret)
		}
	}
	if !strings.Contains(redacted.Error(), "http://"+endpoint) {
		t.Errorf("redacted error %q lost the scheme and host an operator needs", redacted)
	}
	var urlErr *url.Error
	if !errors.As(redacted, &urlErr) || urlErr.Op != "Post" {
		t.Errorf("redacted error %#v is no longer a *url.Error with its operation", redacted)
	}
}

// Wrapping keeps what callers test for: a policy refusal is still ErrBlocked, a
// deadline is still a deadline, and a *url.Error buried under another error's
// text is rewritten inside that text too.
func TestRedactErrorKeepsTheCauseAndReachesAWrappedURLError(t *testing.T) {
	t.Parallel()

	blocked := &url.Error{Op: "Get", URL: "https://api.example.test/v1?token=S3CRET", Err: fmt.Errorf("%w: loopback address 127.0.0.1", safehttp.ErrBlocked)}
	if redacted := safehttp.RedactError(blocked); !errors.Is(redacted, safehttp.ErrBlocked) || strings.Contains(redacted.Error(), "S3CRET") {
		t.Errorf("RedactError(blocked) = %v, want ErrBlocked kept and the query withheld", redacted)
	}

	late := &url.Error{Op: "Get", URL: "https://api.example.test/v1?token=S3CRET", Err: context.DeadlineExceeded}
	if redacted := safehttp.RedactError(late); !errors.Is(redacted, context.DeadlineExceeded) {
		t.Errorf("RedactError(deadline) = %v, want the deadline kept", redacted)
	}

	wrapped := fmt.Errorf("node %q: %w", "Call API", late)
	redacted := safehttp.RedactError(wrapped)
	if strings.Contains(redacted.Error(), "S3CRET") || !strings.HasPrefix(redacted.Error(), `node "Call API": Get "https://api.example.test"`) {
		t.Errorf("RedactError(wrapped) = %q, want the wrapper's text kept and the URL cut to its host", redacted)
	}
	if !errors.Is(redacted, context.DeadlineExceeded) {
		t.Errorf("RedactError(wrapped) = %v, want the deadline still reachable", redacted)
	}

	// Userinfo is a credential too, and a URL that does not parse is withheld
	// whole rather than printed on the chance it holds nothing.
	for _, raw := range []string{"https://ada:PASSW0RD@api.example.test/x", "https://api.example.test/%zz?token=S3CRET"} {
		redacted := safehttp.RedactError(&url.Error{Op: "Get", URL: raw, Err: errors.New("refused")})
		if strings.Contains(redacted.Error(), "PASSW0RD") || strings.Contains(redacted.Error(), "S3CRET") {
			t.Errorf("RedactError(%q) = %q, want the secret withheld", raw, redacted)
		}
	}

	if safehttp.RedactError(nil) != nil {
		t.Error("RedactError(nil) is not nil")
	}
	plain := errors.New("no URL here")
	if safehttp.RedactError(plain) != plain {
		t.Error("RedactError changed an error that carries no *url.Error")
	}
}
