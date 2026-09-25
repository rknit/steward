package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func newRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeRegistry(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(RegistryPath(root), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSaveRegistrySortsAndRoundTrips(t *testing.T) {
	root := newRoot(t)
	paths := []string{"services/api", ".", `we"ird\dir`, "libs/core"}
	if err := SaveRegistry(root, paths); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(RegistryPath(root))
	if err != nil {
		t.Fatal(err)
	}
	want := `projects = [".", "libs/core", "services/api", "we\"ird\\dir"]` + "\n"
	if string(data) != want {
		t.Errorf("file = %q, want %q", data, want)
	}

	// The backslash path is rejected on load, so round-trip only the valid ones.
	valid := []string{"services/api", ".", "libs/core"}
	if err := SaveRegistry(root, valid); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{".", "libs/core", "services/api"}; !slices.Equal(got, want) {
		t.Errorf("LoadRegistry = %q, want %q", got, want)
	}
}

func TestSaveRegistryEmpty(t *testing.T) {
	root := newRoot(t)
	if err := SaveRegistry(root, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(RegistryPath(root))
	if string(data) != "projects = []\n" {
		t.Errorf("file = %q", data)
	}
	entries, _ := os.ReadDir(filepath.Join(root, DirName))
	if len(entries) != 1 {
		t.Errorf("leftover temp files: %v", entries)
	}
}

func TestLoadRegistryErrors(t *testing.T) {
	tests := []struct {
		name, content, wantErr string
	}{
		{"missing key", "", `missing key "projects"`},
		{"unknown key", "projects = []\nextra = 1\n", `unknown key "extra"`},
		{"absolute", `projects = ["/abs"]`, "must be relative"},
		{"escapes", `projects = ["../x"]`, "escapes the root"},
		{"not clean", `projects = ["a/../b"]`, "not clean"},
		{"trailing slash", `projects = ["a/"]`, "not clean"},
		{"empty", `projects = [""]`, "empty"},
		{"backslash", `projects = ["a\\b"]`, "must use /"},
		{"duplicate", `projects = ["a", "a"]`, `duplicate project path "a"`},
		{"bad toml", `projects = [`, "projects.toml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newRoot(t)
			writeRegistry(t, root, tt.content)
			_, err := LoadRegistry(root)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
