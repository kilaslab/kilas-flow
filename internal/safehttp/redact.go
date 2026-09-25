package safehttp

import (
	"errors"
	"net/url"
	"strings"
)

// withheldURL stands in for a URL that could not be parsed. It is printed
// rather than the URL itself on the chance the unparseable part is the secret.
const withheldURL = "(URL withheld)"

// RedactError rewrites every *url.Error in err so the URL it names shows only
// its scheme and host.
//
// Go's *url.Error prints the whole request URL, path and query included, and
// a credential can live in either: httpQueryAuth puts its secret in the query,
// and the Telegram Bot API takes a bot token in the path. Every outbound
// call's transport error is one of these, and it travels on into execution
// records, error items, logs and API answers. The host and port are kept
// because they are what an operator needs to tell which service failed; the
// path, the query and any userinfo are withheld because they are where a
// secret sits.
//
// Every outbound call site passes its transport error through here before it
// returns or logs it, so the rule lives in one place rather than in a copy per
// caller that the next caller forgets.
//
// The result keeps the *url.Error's operation, its Timeout answer and its
// cause, so errors.Is(err, context.DeadlineExceeded) and errors.Is(err,
// ErrBlocked) answer as they did. A *url.Error wrapped inside another error is
// rewritten inside that error's text as well; the wrapper's own type is not
// kept, because its text was fixed when it was made and a new error is the
// only way to change it.
func RedactError(err error) error {
	if err == nil {
		return nil
	}
	if direct, ok := err.(*url.Error); ok {
		return redactURLError(direct)
	}
	var nested *url.Error
	if !errors.As(err, &nested) {
		return err
	}
	redacted := redactURLError(nested)
	text := strings.ReplaceAll(err.Error(), nested.Error(), redacted.Error())
	if nested.URL != "" {
		// A wrapper that formatted the URL on its own, rather than through the
		// *url.Error's text, is caught here.
		text = strings.ReplaceAll(text, nested.URL, redacted.URL)
	}
	return &redactedError{text: text, cause: redacted}
}

// RedactURL renders a URL as its scheme and host alone, for a message.
func RedactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return withheldURL
	}
	if parsed.Scheme == "" {
		return "//" + parsed.Host
	}
	return parsed.Scheme + "://" + parsed.Host
}

// redactURLError copies a *url.Error with its URL cut down, and its cause
// redacted in turn: a redirect refusal wraps the URL it refused.
func redactURLError(err *url.Error) *url.Error {
	return &url.Error{Op: err.Op, URL: RedactURL(err.URL), Err: RedactError(err.Err)}
}

// redactedError is a wrapper's text with the URL inside it cut down.
type redactedError struct {
	text  string
	cause error
}

func (err *redactedError) Error() string { return err.text }

func (err *redactedError) Unwrap() error { return err.cause }
