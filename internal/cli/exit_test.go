package cli

import (
	"errors"
	"net/http"
	"testing"
)

func TestExitCodeMapping(t *testing.T) {
	cases := []struct {
		status  int
		code    int
		errCode string
	}{
		{status: http.StatusOK, code: ExitOK, errCode: ""},
		{status: http.StatusCreated, code: ExitOK, errCode: ""},
		{status: http.StatusAccepted, code: ExitOK, errCode: ""},
		{status: http.StatusNoContent, code: ExitOK, errCode: ""},
		{status: http.StatusBadRequest, code: ExitUsage, errCode: "bad_request"},
		{status: http.StatusUnprocessableEntity, code: ExitUsage, errCode: "bad_request"},
		{status: http.StatusUnauthorized, code: ExitRefused, errCode: "unauthenticated"},
		{status: http.StatusForbidden, code: ExitRefused, errCode: "scope_denied"},
		{status: http.StatusNotFound, code: ExitNotFound, errCode: "not_found"},
		{status: http.StatusConflict, code: ExitConflict, errCode: "conflict"},
		{status: http.StatusTooManyRequests, code: ExitNotReady, errCode: "rate_limited"},
		{status: http.StatusServiceUnavailable, code: ExitNotReady, errCode: "not_ready"},
		{status: http.StatusInternalServerError, code: ExitFailure, errCode: "server_error"},
		{status: http.StatusBadGateway, code: ExitFailure, errCode: "server_error"},
	}

	for _, tc := range cases {
		code, errCode := exitForStatus(tc.status)
		if code != tc.code || errCode != tc.errCode {
			t.Errorf("exitForStatus(%d) = (%d, %q), want (%d, %q)", tc.status, code, errCode, tc.code, tc.errCode)
		}
	}
}

func TestExitErrorIsAnError(t *testing.T) {
	err := &ExitError{Code: ExitRefused, ErrCode: "confirmation_required", Message: "activate publishes a public endpoint"}
	if err.Error() != "activate publishes a public endpoint" {
		t.Fatalf("Error() = %q", err.Error())
	}

	var target *ExitError
	if !errors.As(error(err), &target) {
		t.Fatal("errors.As did not recover the ExitError")
	}
}

func TestScopeDeniedIsDistinguishableFromEveryOtherRefusal(t *testing.T) {
	// A 403 and a 401 both exit 3, so error.code is the only thing that tells
	// an agent "you may not" apart from "you did not say who you are".
	cases := []struct {
		name    string
		status  int
		errCode string
	}{
		{name: "scope denied", status: http.StatusForbidden, errCode: "scope_denied"},
		{name: "unauthenticated", status: http.StatusUnauthorized, errCode: "unauthenticated"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, errCode := exitForStatus(tc.status)
			if code != ExitRefused {
				t.Fatalf("exit = %d, want %d", code, ExitRefused)
			}
			if errCode != tc.errCode {
				t.Fatalf("error.code = %q, want %q", errCode, tc.errCode)
			}
		})
	}

	// A refusal's exit code is distinct from every other outcome an agent is
	// told to treat differently.
	refused, _ := exitForStatus(http.StatusForbidden)
	for _, status := range []int{http.StatusNotFound, http.StatusConflict, http.StatusServiceUnavailable, http.StatusInternalServerError, http.StatusOK} {
		if other, _ := exitForStatus(status); other == refused {
			t.Fatalf("status %d exits %d, the same as a refusal", status, other)
		}
	}
}

func TestExitCodeConstantsAreTheDocumentedContract(t *testing.T) {
	const (
		wantOK       = 0
		wantError    = 1
		wantUsage    = 2
		wantRefused  = 3
		wantNotFound = 4
		wantConflict = 5
		wantNotReady = 6
	)

	if ExitOK != wantOK || ExitFailure != wantError || ExitUsage != wantUsage || ExitRefused != wantRefused ||
		ExitNotFound != wantNotFound || ExitConflict != wantConflict || ExitNotReady != wantNotReady {
		t.Fatalf("exit codes changed: %d %d %d %d %d %d %d",
			ExitOK, ExitFailure, ExitUsage, ExitRefused, ExitNotFound, ExitConflict, ExitNotReady)
	}
}
