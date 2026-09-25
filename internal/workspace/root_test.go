package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFindRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, cwd := range []string{root, nested} {
		got, err := FindRoot(cwd)
		if err != nil {
			t.Fatalf("FindRoot(%q): %v", cwd, err)
		}
		if got != root {
			t.Errorf("FindRoot(%q) = %q, want %q", cwd, got, root)
		}
	}
}

func TestFindRootIgnoresStewFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, DirName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := FindRoot(root); !errors.Is(err, ErrNotWorkspace) {
		t.Fatalf("err = %v, want ErrNotWorkspace", err)
	}
}

func TestFindRootNotWorkspace(t *testing.T) {
	if _, err := FindRoot(t.TempDir()); !errors.Is(err, ErrNotWorkspace) {
		t.Fatalf("err = %v, want ErrNotWorkspace", err)
	}
}

func TestFindRootFromInsideStewDir(t *testing.T) {
	root := t.TempDir()
	runs := filepath.Join(root, DirName, "runs", "20260925T043601Z-3f9a")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := FindRoot(runs); err != nil || got != root {
		t.Fatalf("FindRoot(%q) = %q, %v; want %q", runs, got, err, root)
	}
}
