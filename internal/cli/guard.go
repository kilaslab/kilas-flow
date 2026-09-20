// The --yes guard: the one place a verb's own metadata can stop an invocation
// before it does anything.
//
// Design §4.7 is the contract: a guarded verb refuses without --yes, exits 3
// with error.code "confirmation_required", and never infers consent from
// --json, from --quiet, or from stdout not being a terminal. Automation that
// means it says so.
package cli

// confirmationCode is the error.code a guarded verb refuses with.
//
// It sits beside the two the HTTP layer produces, `scope_denied` (403) and
// `unauthenticated` (401), and it is deliberately a third name: all three exit
// 3, so an agent that only reads the exit code cannot tell "I am not
// authenticated" from "I may not" from "I was not confirmed", and the error
// code is what makes the difference actionable. `scope_denied` means drop the
// step, `unauthenticated` means get a credential, and this one means ask the
// user.
const confirmationCode = "confirmation_required"

// requireConfirmation refuses a guarded verb that was not explicitly
// confirmed.
//
// The environment is deliberately not consulted. A non-TTY, a --json envelope
// and a --quiet line are all the shapes automation uses, and every one of them
// is exactly the situation where a confirmation must not be assumed: the flag
// is the whole signal, and it is only ever passed on an explicit instruction
// from the user. The Env parameter is here because Run already has it and a
// future prompt would need stdin; it is not a source of consent.
//
// A guarded verb is guaranteed to carry a refusal sentence (the registry test
// asserts it), so the message always says what the verb would do.
func requireConfirmation(v Verb, yes bool, _ Env) error {
	if !v.Guarded || yes {
		return nil
	}

	return &ExitError{
		Code:    ExitRefused,
		ErrCode: confirmationCode,
		Message: v.Path + " " + v.Refusal + "; pass --yes to confirm",
	}
}
