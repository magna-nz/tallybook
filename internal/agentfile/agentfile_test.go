package agentfile

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseFrontmatter(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".claude", "agents")
	write(t, dir, "researcher.md", "---\nname: researcher\ndescription: reads things\ntools: Read, Grep, Glob\nmodel: sonnet\n---\n\nYou are a researcher. This body is prompt text and must not be read.\n")
	write(t, dir, "implementer.md", "---\nname: implementer\nmodel: inherit\n---\nbody\n")
	write(t, dir, "plain.md", "---\nname: plain\n---\nbody\n")
	write(t, dir, "notanagent.md", "no frontmatter here\n")

	set := Load(root)

	r, ok := set.Lookup("researcher")
	if !ok {
		t.Fatal("researcher not found")
	}
	if r.Model != "sonnet" || !r.SetsModel() {
		t.Errorf("researcher model = %q, SetsModel = %v", r.Model, r.SetsModel())
	}
	if r.Tools != "Read, Grep, Glob" {
		t.Errorf("tools = %q", r.Tools)
	}
	if r.Scope != Project {
		t.Errorf("scope = %q, want project", r.Scope)
	}

	i, _ := set.Lookup("IMPLEMENTER") // lookup is case-insensitive
	if i.Model != "inherit" {
		t.Errorf("implementer model = %q", i.Model)
	}
	if i.SetsModel() {
		t.Error(`"inherit" asks for the caller's model, so it does not pin one`)
	}

	p, ok := set.Lookup("plain")
	if !ok || p.SetsModel() {
		t.Errorf("plain: ok=%v model=%q, want found with no model", ok, p.Model)
	}
	if _, ok := set.Lookup("notanagent"); ok {
		t.Error("a file without frontmatter is not an agent definition")
	}
}

func TestProjectShadowsUser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, filepath.Join(home, ".claude", "agents"), "researcher.md", "---\nname: researcher\nmodel: opus\n---\n")

	root := t.TempDir()
	write(t, filepath.Join(root, ".claude", "agents"), "researcher.md", "---\nname: researcher\nmodel: haiku\n---\n")

	got, ok := Load(root).Lookup("researcher")
	if !ok {
		t.Fatal("researcher not found")
	}
	if got.Model != "haiku" || got.Scope != Project {
		t.Errorf("got model %q scope %q, want the project file to win", got.Model, got.Scope)
	}
}

func TestMissingDirectoriesAreNotAnError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	set := Load(filepath.Join(t.TempDir(), "nope"), "")
	if len(set) != 0 {
		t.Errorf("expected an empty set, got %v", set)
	}
	if _, ok := set.Lookup("anything"); ok {
		t.Error("empty set should find nothing")
	}
	var nilSet Set
	if _, ok := nilSet.Lookup("anything"); ok {
		t.Error("a nil set must be usable")
	}
}

func TestBodyIsNeverRead(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".claude", "agents"), "a.md",
		"---\nname: a\nmodel: sonnet\n---\nSECRET PROMPT TEXT\nmodel: haiku\n")
	a, _ := Load(root).Lookup("a")
	if a.Model != "sonnet" {
		t.Errorf("model = %q: parsing must stop at the closing --- and never read the body", a.Model)
	}
}
