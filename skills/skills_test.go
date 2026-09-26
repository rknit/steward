package skills

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallWritesEverySkillFile(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), ".agents", "skills")
	path, err := Install(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, Name); path != want {
		t.Fatalf("Install returned %q, want %q", path, want)
	}
	assertInstalled(t, path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("%s holds %d entries, want only %s", dir, len(entries), Name)
	}
}

func TestInstallReplacesAnEarlierCopy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, Name)
	if err := os.MkdirAll(filepath.Join(path, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SKILL.md", "stale.md", "references/stale.md"} {
		if err := os.WriteFile(filepath.Join(path, name), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Install(dir); err != nil {
		t.Fatal(err)
	}
	assertInstalled(t, path)
}

func TestInstallLeavesOtherSkillsAlone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	other := filepath.Join(dir, "other", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall(dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(other)
	if err != nil || string(got) != "other" {
		t.Fatalf("other skill = %q, %v; want it untouched", got, err)
	}
}

func TestUninstallRemovesTheSkill(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := Install(dir); err != nil {
		t.Fatal(err)
	}
	path, err := Uninstall(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, Name); path != want {
		t.Fatalf("Uninstall returned %q, want %q", path, want)
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s still exists after Uninstall (Lstat: %v)", path, err)
	}
}

func TestUninstallWithoutTheSkill(t *testing.T) {
	t.Parallel()
	_, err := Uninstall(t.TempDir())
	if !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Uninstall of an empty dir = %v, want ErrNotInstalled", err)
	}
}

// assertInstalled checks that path holds exactly the embedded skill, byte for byte.
func assertInstalled(t *testing.T, path string) {
	t.Helper()
	want := map[string][]byte{}
	err := fs.WalkDir(files, Name, func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := files.ReadFile(name)
		want[name[len(Name)+1:]] = data
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := want["SKILL.md"]; !ok {
		t.Fatalf("embedded skill has no SKILL.md: %v", want)
	}
	got := map[string][]byte{}
	err = filepath.WalkDir(path, func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(path, name)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(name)
		got[filepath.ToSlash(rel)] = data
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("%s is missing", name)
		} else if !bytes.Equal(got[name], data) {
			t.Errorf("%s differs from the embedded copy", name)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("%s is not part of the skill", name)
		}
	}
}
