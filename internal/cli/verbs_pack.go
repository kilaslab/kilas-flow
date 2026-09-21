package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/kilaslab/kilas-flow/internal/nodepack"
)

// packVerbs are the pack verbs: `pack validate` is the only one.
//
// It is local — no HTTP, no server, Operation "" — because a pack is a file on
// the machine the command runs on and the check an author needs before
// installing it. It wraps internal/nodepack rather than reimplementing the
// rules, so what the verb accepts is what the server would register: the
// validator runs the real loader and the real registration against throwaway
// registries.
//
// Deliberately absent: `pack install`. Design §4.2 marks it guarded, but the
// pack surface is a local loader with **no server operation behind it**, so
// there is nothing for a verb to drive: a guarded verb's two gates — --yes and
// a tenant-wide key — protect a server operation, and an install has none.
func packVerbs() []Verb {
	return []Verb{
		{
			Path:    "pack validate",
			Summary: "check a pack directory or a pack.json the way the server would load it",
			Run:     runPackValidate,
			Human:   humanPackValidation,
		},
	}
}

// packIssue is one problem as the envelope carries it.
//
// Severity is always "error": the validator runs the real loader and registers
// against throwaway registries, so every issue it returns is one that would stop
// an install. The field is carried anyway so that a future warning-level finding
// does not have to change the shape a caller parses.
type packIssue struct {
	Severity string `json:"severity"`
	File     string `json:"file"`
	Path     string `json:"path"`
	Message  string `json:"message"`
}

// packValidation is the payload of `pack validate`.
type packValidation struct {
	Path   string      `json:"path"`
	OK     bool        `json:"ok"`
	Issues []packIssue `json:"issues"`
}

// runPackValidate checks one pack directory or one manifest file.
//
// A missing path is a usage error rather than an "invalid pack": nothing was
// checked, so the caller fixed nothing by being told the pack is bad.
func runPackValidate(ctx *Context, args []string) error {
	path, err := requireOneID(args, "pack directory or pack.json")
	if err != nil {
		return err
	}

	info, err := os.Stat(path)
	if err != nil {
		return usageError("cannot read %s: %v", path, err)
	}

	var issues []nodepack.Issue
	if info.IsDir() {
		// ValidateDir reads one installed pack directory — the checksum sidecar
		// first, then the manifest — and names the pack by the directory it
		// sits in. The absolute path is what makes `pack validate .` mean the
		// directory the caller is standing in rather than its parent.
		absolute, err := filepath.Abs(path)
		if err != nil {
			return usageError("cannot resolve %s: %v", path, err)
		}
		issues = nodepack.ValidateDir(filepath.Dir(absolute), filepath.Base(absolute))
	} else {
		issues = nodepack.ValidateFile(path)
	}

	report := packValidation{Path: path, OK: len(issues) == 0, Issues: packIssues(issues)}
	ctx.Data = report
	ctx.Primary = strconv.FormatBool(report.OK)

	if report.OK {
		return nil
	}

	// The exit code is the point: an author and an agent both need to notice
	// without parsing English, and a pipeline that installs a pack must stop.
	// The whole list travels in error.detail.issues, so nothing is lost to a
	// one-line message.
	encoded, err := json.Marshal(report.Issues)
	if err != nil {
		return &ExitError{Code: ExitFailure, ErrCode: "error", Message: "could not encode the validation report: " + err.Error()}
	}

	return &ExitError{
		Code:    ExitFailure,
		ErrCode: "invalid_pack",
		Message: fmt.Sprintf("%s is not a valid pack: %d problem(s), the first is %s", path, len(report.Issues), issues[0].Error()),
		Issues:  encoded,
	}
}

// packIssues projects the validator's issues onto the wire shape.
func packIssues(issues []nodepack.Issue) []packIssue {
	projected := make([]packIssue, 0, len(issues))
	for _, issue := range issues {
		projected = append(projected, packIssue{
			Severity: "error",
			File:     issue.File,
			Path:     issue.Path,
			Message:  issue.Message,
		})
	}

	return projected
}

// humanPackValidation prints the verdict and then every problem, because the
// reason to run the verb by hand is to read them.
func humanPackValidation(w io.Writer, data any) {
	report, ok := data.(packValidation)
	if !ok {
		printJSONValue(w, data)

		return
	}

	printKV(w, [][2]string{
		{"path", report.Path},
		{"ok", strconv.FormatBool(report.OK)},
		{"issues", strconv.Itoa(len(report.Issues))},
	})
	for _, issue := range report.Issues {
		fmt.Fprintf(w, "%s: %s: %s: %s\n", issue.Severity, issue.File, issue.Path, issue.Message)
	}
}
