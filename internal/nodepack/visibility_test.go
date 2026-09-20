package nodepack_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/nodepack"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

// These tests cover the author-facing half of node visibility: the manifest
// field a pack declares its scope with, and the operator override that replaces
// it. The registry's own rules live in internal/node.
//
// The policy is safehttp.DefaultPolicy(), never the file's localPolicy(): a
// pack test has no reason to reach a private network, and MEMORY.md forbids
// relaxing that to make a test pass.

// telegramPack decodes the shipped manifest into a Pack, so a test can change
// one field and re-render a manifest that is otherwise the real thing.
func telegramPack(t *testing.T) *nodepack.Pack {
	t.Helper()
	pack, err := nodepack.Decode(telegramManifest(t))
	if err != nil {
		t.Fatalf("Decode(pack.json) error = %v", err)
	}
	return pack
}

// manifestWithVisibleTo renders the Telegram manifest carrying visibleTo.
//
// Marshalling the decoded pack rather than patching bytes keeps every other
// field exactly as shipped; VisibleTo is omitempty, so an empty list is written
// by manifestWithRawVisibleTo instead.
func manifestWithVisibleTo(t *testing.T, tenants []string) []byte {
	t.Helper()
	pack := telegramPack(t)
	pack.VisibleTo = tenants
	encoded, err := json.Marshal(pack)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return encoded
}

// manifestWithRawVisibleTo writes the value of visibleTo verbatim, which is what
// the empty-list case needs: omitempty cannot produce `"visibleTo":[]`.
func manifestWithRawVisibleTo(t *testing.T, raw string) []byte {
	t.Helper()
	manifest := telegramManifest(t)
	if !strings.HasPrefix(string(manifest), "{") {
		t.Fatal("the shipped manifest is not a JSON object")
	}
	return append([]byte(`{"visibleTo":`+raw+","), manifest[1:]...)
}

func issueAt(issues []nodepack.Issue, path string) (nodepack.Issue, bool) {
	for _, issue := range issues {
		if issue.Path == path {
			return issue, true
		}
	}
	return nodepack.Issue{}, false
}

// A pack that ships nodes for one customer declares that in its manifest, and
// the declaration survives registration: the catalogue a compiler is given for
// that tenant holds the node, and the catalogue for anyone else does not.
func TestAPackManifestDeclaresWhoMaySeeIt(t *testing.T) {
	t.Parallel()

	pack := telegramPack(t)
	pack.VisibleTo = []string{"acme"}

	set := installDirDeps(safehttp.DefaultPolicy())
	if err := nodepack.Register(set.definitions, set.routes, set.executors, set.options, pack); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	version := pack.Version
	if definition, found := set.definitions.Get(pack.Type, version); !found {
		t.Fatalf("%s v%s did not register", pack.Type, version)
	} else if !reflect.DeepEqual(definition.VisibleTo, []string{"acme"}) {
		t.Fatalf("VisibleTo = %v, want [acme]", definition.VisibleTo)
	}

	if _, found := set.definitions.ForTenant("acme").Lookup(pack.Type, version); !found {
		t.Error("acme's catalogue does not hold a pack scoped to acme")
	}
	if _, found := set.definitions.ForTenant("globex").Lookup(pack.Type, version); found {
		t.Error("globex's catalogue holds a pack scoped to acme")
	}
	if got := set.definitions.Scopes()[pack.Type]; !reflect.DeepEqual(got, []string{"acme"}) {
		t.Errorf("Scopes()[%s] = %v, want [acme]", pack.Type, got)
	}
	// The plain registry is the deployment's catalogue and is never narrowed.
	if _, found := set.definitions.Lookup(pack.Type, version); !found {
		t.Error("the plain registry lost the pack")
	}
}

// An empty list would read either as "nobody" or as "everybody", and a manifest
// that cannot say what it means is refused rather than guessed at.
func TestAnEmptyVisibleToIsRefusedNotTreatedAsEveryone(t *testing.T) {
	t.Parallel()

	manifest := manifestWithRawVisibleTo(t, "[]")
	pack, err := nodepack.Decode(manifest)
	if err != nil {
		t.Fatalf("Decode() error = %v, want the field itself to decode", err)
	}
	if _, _, err := nodepack.Load(pack); err == nil {
		t.Fatal("Load() accepted an empty visibleTo")
	} else if !strings.Contains(err.Error(), "visibleTo") {
		t.Errorf("Load() error = %v, want it to name visibleTo", err)
	} else if !strings.Contains(err.Error(), "omit") {
		t.Errorf("Load() error = %v, want it to tell the author to omit the field", err)
	}

	issues := nodepack.ValidateBytes(manifest, "pack.json")
	// The loader reports the same problem against the whole document, as it
	// does for every structural error; the path is what an author needs.
	if _, found := issueAt(issues, "$.visibleTo"); !found {
		t.Errorf("ValidateBytes() issues = %#v, want one at $.visibleTo", issues)
	}

	// The other direction: an absent field is unscoped, not an error.
	plain := telegramPack(t)
	if plain.VisibleTo != nil {
		t.Fatalf("the shipped manifest declares VisibleTo = %v, want none", plain.VisibleTo)
	}
	if _, _, err := nodepack.Load(plain); err != nil {
		t.Fatalf("Load() of a manifest without visibleTo error = %v, want accepted", err)
	}
}

// A tenant ID that cannot appear in a scope is reported at the entry that
// broke, not at the field: `$.visibleTo` alone would leave the author counting
// list entries.
func TestValidateNamesTheBadTenantEntry(t *testing.T) {
	t.Parallel()

	manifest := manifestWithVisibleTo(t, []string{"acme", "a b"})
	issues := nodepack.ValidateBytes(manifest, "pack.json")

	issue, found := issueAt(issues, "$.visibleTo[1]")
	if !found {
		t.Fatalf("ValidateBytes() issues = %#v, want one at $.visibleTo[1]", issues)
	}
	if !strings.Contains(issue.Message, `"a b"`) {
		t.Errorf("issue message = %q, want it to quote the offending entry", issue.Message)
	}
	if _, found := issueAt(issues, "$.visibleTo[0]"); found {
		t.Error("the good entry was reported too")
	}
}

// packKeys is a hand-listed copy of the struct's fields, and the way it fails
// is silent: a field added to Pack is refused as unknown by a checker whose
// only job is to be right about the format. So every json tag on Pack is tried
// against the checker itself.
func TestEveryPackFieldIsAKnownManifestKey(t *testing.T) {
	t.Parallel()

	packType := reflect.TypeOf(nodepack.Pack{})
	tags := make([]string, 0, packType.NumField())
	for index := range packType.NumField() {
		tag, _, _ := strings.Cut(packType.Field(index).Tag.Get("json"), ",")
		if tag == "" || tag == "-" {
			continue
		}
		tags = append(tags, tag)
	}
	if len(tags) < 10 {
		t.Fatalf("reflected only %d json tags off nodepack.Pack: %v", len(tags), tags)
	}

	for _, tag := range tags {
		issues := nodepack.ValidateBytes([]byte(`{"`+tag+`":null}`), "pack.json")
		for _, issue := range issues {
			if strings.Contains(issue.Message, "unknown field") {
				t.Errorf("field %q is not in packKeys: %s", tag, issue.Error())
			}
		}
	}

	// The positive control: the assertion above is worthless unless a field the
	// format does not have is reported.
	issues := nodepack.ValidateBytes([]byte(`{"visibleToTenants":null}`), "pack.json")
	found := false
	for _, issue := range issues {
		if strings.Contains(issue.Message, "unknown field") {
			found = true
		}
	}
	if !found {
		t.Errorf("ValidateBytes() issues = %#v, want an unknown field reported", issues)
	}
}

// The declaration has to survive the directory loader, checksum and all: that
// is the install path an operator uses for a pack that is not embedded.
func TestLoadDirHonoursVisibleTo(t *testing.T) {
	t.Parallel()

	manifest := manifestWithVisibleTo(t, []string{"acme"})
	dir := t.TempDir()
	writePack(t, dir, "telegram", manifest)

	set := installDirDeps(safehttp.DefaultPolicy())
	if err := nodepack.LoadDir(set.deps(), dir); err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}

	version := telegramPack(t).Version
	if _, found := set.definitions.ForTenant("acme").Lookup("pack.telegram", version); !found {
		t.Error("acme does not see the directory-loaded pack it was scoped to")
	}
	if _, found := set.definitions.ForTenant("globex").Lookup("pack.telegram", version); found {
		t.Error("globex sees a directory-loaded pack scoped to acme")
	}
	if got := set.definitions.Scopes()["pack.telegram"]; !reflect.DeepEqual(got, []string{"acme"}) {
		t.Errorf("Scopes()[pack.telegram] = %v, want [acme]", got)
	}
}

// The override exists so an operator can move a pack to another tenant without
// editing a manifest whose bytes are pinned by a checksum.
func TestAnOperatorOverrideBeatsTheManifest(t *testing.T) {
	t.Parallel()

	manifest := manifestWithVisibleTo(t, []string{"acme"})
	dir := t.TempDir()
	writePack(t, dir, "telegram", manifest)

	set := installDirDeps(safehttp.DefaultPolicy())
	if err := nodepack.LoadDir(set.deps(), dir); err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}

	if err := set.definitions.ApplyVisibility(map[string][]string{"pack.telegram": {"globex"}}); err != nil {
		t.Fatalf("ApplyVisibility() error = %v", err)
	}

	version := telegramPack(t).Version
	if _, found := set.definitions.ForTenant("globex").Lookup("pack.telegram", version); !found {
		t.Error("globex does not see the pack the override gave it")
	}
	if _, found := set.definitions.ForTenant("acme").Lookup("pack.telegram", version); found {
		t.Error("acme still sees the pack: the override replaces the manifest's set")
	}
	if got := set.definitions.Scopes()["pack.telegram"]; !reflect.DeepEqual(got, []string{"globex"}) {
		t.Errorf("Scopes()[pack.telegram] = %v, want [globex]", got)
	}
	// The override writes every stored version, so the plain registry agrees.
	definition, found := set.definitions.Get("pack.telegram", version)
	if !found || !reflect.DeepEqual(definition.VisibleTo, []string{"globex"}) {
		t.Errorf("Get() = %#v, want VisibleTo [globex]", definition.VisibleTo)
	}
}
