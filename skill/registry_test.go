// ABOUTME: Tests for the skill Registry: directory loading, duplicate detection,
// ABOUTME: accessors, and catalog rendering.
package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkillDir creates <root>/<name>/SKILL.md with the given frontmatter + body.
func writeSkillDir(t *testing.T, root, name, desc, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDir(t *testing.T) {
	root := t.TempDir()
	writeSkillDir(t, root, "beta", "Second skill.", "do beta")
	writeSkillDir(t, root, "alpha", "First skill.", "do alpha")
	// A subdirectory without a SKILL.md is ignored.
	if err := os.MkdirAll(filepath.Join(root, "notaskill"), 0o755); err != nil {
		t.Fatal(err)
	}

	reg, err := LoadDir(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg.Count() != 2 {
		t.Fatalf("Count = %d, want 2", reg.Count())
	}
	if got := reg.List(); got[0] != "alpha" || got[1] != "beta" {
		t.Errorf("List = %v, want [alpha beta]", got)
	}
	s, ok := reg.Get("alpha")
	if !ok || s.Body != "do alpha" {
		t.Errorf("Get(alpha) = %+v, %v", s, ok)
	}
}

func TestLoadDirMissingDirErrors(t *testing.T) {
	if _, err := LoadDir(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Error("expected error for missing directory")
	}
}

func TestLoadDirDuplicateNameErrors(t *testing.T) {
	root := t.TempDir()
	// Two different directories whose frontmatter declares the same name.
	writeSkillDir(t, root, "dir-one", "First.", "body one")
	dir2 := filepath.Join(root, "dir-two")
	if err := os.MkdirAll(dir2, 0o755); err != nil {
		t.Fatal(err)
	}
	dup := "---\nname: dir-one\ndescription: Clash.\n---\n\nbody two\n"
	if err := os.WriteFile(filepath.Join(dir2, "SKILL.md"), []byte(dup), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDir(root); err == nil {
		t.Error("expected duplicate-name error")
	}
}

func TestLoadDirMalformedSkillErrors(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "broken")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("no frontmatter here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDir(root); err == nil {
		t.Error("expected parse error to propagate")
	}
}

func TestCatalog(t *testing.T) {
	root := t.TempDir()
	writeSkillDir(t, root, "beta", "Second skill.", "do beta")
	writeSkillDir(t, root, "alpha", "First skill.", "do alpha")
	reg, err := LoadDir(root)
	if err != nil {
		t.Fatal(err)
	}

	cat := reg.Catalog()
	if !strings.Contains(cat, "## Available Skills") {
		t.Errorf("catalog missing header:\n%s", cat)
	}
	if !strings.Contains(cat, "- **alpha** — First skill.") {
		t.Errorf("catalog missing alpha entry:\n%s", cat)
	}
	if !strings.Contains(cat, "- **beta** — Second skill.") {
		t.Errorf("catalog missing beta entry:\n%s", cat)
	}
	// alpha sorts before beta.
	if strings.Index(cat, "alpha") > strings.Index(cat, "beta") {
		t.Errorf("catalog not sorted:\n%s", cat)
	}
}

func TestCatalogEmpty(t *testing.T) {
	if got := NewRegistry().Catalog(); got != "" {
		t.Errorf("empty catalog = %q, want \"\"", got)
	}
}
