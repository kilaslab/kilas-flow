package main

import (
	"strings"
	"testing"
)

// The default is today's behaviour: parsing nothing selects both, so an
// upgrade that sets no flag runs API, workers and scheduler in one process
// exactly as before. "workers" spells the ticket's wording and means worker.
func TestParseProcessRoleDefaultsAndAcceptsThePlural(t *testing.T) {
	for input, want := range map[string]processRole{
		"":        processRoleBoth,
		"both":    processRoleBoth,
		"BOTH":    processRoleBoth,
		"api":     processRoleAPI,
		"API":     processRoleAPI,
		"worker":  processRoleWorker,
		"workers": processRoleWorker,
	} {
		got, err := parseProcessRole(input)
		if err != nil || got != want {
			t.Errorf("parseProcessRole(%q) = (%q, %v), want (%q, nil)", input, got, err, want)
		}
	}
	if _, err := parseProcessRole("scheduler"); err == nil {
		t.Error("parseProcessRole(scheduler) accepted a role that runs nothing by itself")
	}
}

// One binary, three shapes: both runs everything, api runs the API and the
// scheduler without workers, worker runs workers with neither API nor
// scheduler. The scheduler stays single by role; several api/both processes
// stay safe anyway because ClaimDue advances the due time inside the same
// transaction that reads it.
func TestProcessRoleMatrixMatchesTheSingleBinaryContract(t *testing.T) {
	if role := processRoleBoth; !role.runsAPI() || !role.runsWorkers() || !role.runsScheduler() {
		t.Errorf("both = api:%v workers:%v scheduler:%v, want all true", role.runsAPI(), role.runsWorkers(), role.runsScheduler())
	}
	if role := processRoleAPI; !role.runsAPI() || role.runsWorkers() || !role.runsScheduler() {
		t.Errorf("api = api:%v workers:%v scheduler:%v, want true/false/true", role.runsAPI(), role.runsWorkers(), role.runsScheduler())
	}
	if role := processRoleWorker; role.runsAPI() || !role.runsWorkers() || role.runsScheduler() {
		t.Errorf("worker = api:%v workers:%v scheduler:%v, want false/true/false", role.runsAPI(), role.runsWorkers(), role.runsScheduler())
	}
}

// SQLite holds one writer, no LISTEN/NOTIFY and one file: a split role on it
// is refused at startup with an explanation, not left to fail under load.
// PostgreSQL hosts every shape.
func TestSplitRoleOnSQLiteIsRefusedAtStartup(t *testing.T) {
	if err := validateRoleForDriver(processRoleBoth, "sqlite"); err != nil {
		t.Errorf("both on sqlite = %v, want nil: the default stays single-process", err)
	}
	for _, role := range []processRole{processRoleAPI, processRoleWorker} {
		err := validateRoleForDriver(role, "sqlite")
		if err == nil {
			t.Errorf("%s on sqlite was accepted, want a refusal naming the driver", role)
			continue
		}
		if !strings.Contains(err.Error(), "sqlite") || !strings.Contains(err.Error(), "postgres") {
			t.Errorf("%s on sqlite error = %q, want it to name both drivers so the fix is obvious", role, err)
		}
	}
	for _, role := range []processRole{processRoleBoth, processRoleAPI, processRoleWorker} {
		if err := validateRoleForDriver(role, "postgres"); err != nil {
			t.Errorf("%s on postgres = %v, want nil", role, err)
		}
	}
}
