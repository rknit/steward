package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFindRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, cwd := range []string{root, nested} {
		got, err := FindRoot(cwd, nil)
		if err != nil {
			t.Fatalf("FindRoot(%q): %v", cwd, err)
		}
		if got != root {
			t.Errorf("FindRoot(%q) = %q, want %q", cwd, got, root)
		}
	}
}

func TestFindRootIgnoresStewFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, DirName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := FindRoot(root, []string{filepath.Dir(root)}); !errors.Is(err, ErrNotWorkspace) {
		t.Fatalf("err = %v, want ErrNotWorkspace", err)
	}
}

func TestFindRootNotWorkspace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := FindRoot(dir, []string{filepath.Dir(dir)}); !errors.Is(err, ErrNotWorkspace) {
		t.Fatalf("err = %v, want ErrNotWorkspace", err)
	}
}

func TestFindRootFromInsideStewDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	runs := filepath.Join(root, DirName, "runs", "20260925T043601Z-3f9a")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := FindRoot(runs, nil); err != nil || got != root {
		t.Fatalf("FindRoot(%q) = %q, %v; want %q", runs, got, err, root)
	}
}

func TestFindRootCeilings(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	inner := filepath.Join(root, "inner")
	if err := os.MkdirAll(filepath.Join(root, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		cwd      string
		ceilings []string
		want     string
	}{
		{"ceiling above the root", inner, []string{filepath.Dir(root)}, root},
		{"ceiling at the root", inner, []string{root}, ""},
		{"ceiling with a trailing slash", inner, []string{"/unrelated", root + "/"}, ""},
		{"cwd is the ceiling", root, []string{root}, root},
		{"relative and empty ceilings", inner, []string{"", "inner", ".", ".."}, root},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := FindRoot(tt.cwd, tt.ceilings)
			if tt.want == "" {
				if !errors.Is(err, ErrNotWorkspace) {
					t.Fatalf("FindRoot(%q, %q) = %q, %v; want ErrNotWorkspace", tt.cwd, tt.ceilings, got, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("FindRoot(%q, %q) = %q, %v; want %q", tt.cwd, tt.ceilings, got, err, tt.want)
			}
		})
	}
}
