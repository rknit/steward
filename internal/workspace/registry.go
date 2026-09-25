package workspace

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// RegistryFile is the registry's file name inside .stew/.
const RegistryFile = "projects.toml"

// RegistryPath returns the path of the registry file for a workspace root.
func RegistryPath(root string) string {
	return filepath.Join(root, DirName, RegistryFile)
}

type rawRegistry struct {
	Projects []string `toml:"projects"`
}

// LoadRegistry reads .stew/projects.toml and returns the registered project paths.
func LoadRegistry(root string) ([]string, error) {
	file := RegistryPath(root)
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var raw rawRegistry
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		return nil, fmt.Errorf("%s: unknown key %q", file, keys[0].String())
	}
	if !md.IsDefined("projects") {
		return nil, fmt.Errorf("%s: missing key %q", file, "projects")
	}
	seen := make(map[string]bool, len(raw.Projects))
	for _, p := range raw.Projects {
		if err := ValidateProjectPath(p); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		if seen[p] {
			return nil, fmt.Errorf("%s: duplicate project path %q", file, p)
		}
		seen[p] = true
	}
	return raw.Projects, nil
}

// ValidateProjectPath checks that p is a clean, root-relative, slash-separated path that stays inside the root.
func ValidateProjectPath(p string) error {
	switch {
	case p == "":
		return fmt.Errorf("invalid project path %q: empty", p)
	case strings.Contains(p, `\`):
		return fmt.Errorf("invalid project path %q: must use /", p)
	case path.IsAbs(p):
		return fmt.Errorf("invalid project path %q: must be relative", p)
	case path.Clean(p) != p:
		return fmt.Errorf("invalid project path %q: not clean (want %q)", p, path.Clean(p))
	case p == ".." || strings.HasPrefix(p, "../"):
		return fmt.Errorf("invalid project path %q: escapes the root", p)
	}
	return nil
}

// SaveRegistry writes the registry sorted, through a temp file in .stew/ and a rename.
func SaveRegistry(root string, paths []string) error {
	sorted := slices.Clone(paths)
	slices.Sort(sorted)

	var buf bytes.Buffer
	buf.WriteString("projects = [")
	for i, p := range sorted {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(quote(p))
	}
	buf.WriteString("]\n")

	return writeFileAtomic(RegistryPath(root), buf.Bytes())
}

func writeFileAtomic(file string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(file), "."+filepath.Base(file)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}

// quote renders s as a TOML basic string.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
