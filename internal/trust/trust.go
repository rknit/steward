// Package trust runs the commands that make a wrapper usable in a new tree, and records in .stew/trust.json which
// ran there.
package trust

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"

	"github.com/rknit/steward/internal/runner"
	"github.com/rknit/steward/internal/workspace"
)

// File is the trust record's file name inside .stew/.
const File = "trust.json"

// Workspace is the name of the workspace's entry.
const Workspace = "workspace"

// Entry is one trust command: the workspace's or a project's.
type Entry struct {
	Name    string // Workspace, or the project name
	Path    string // the project's registered path; "" for the workspace
	Command string
}

// Entries returns the workspace's entry, then each project's by name. A trust command "" has no entry.
func Entries(ws *workspace.Workspace) []Entry {
	var entries []Entry
	if ws.Trust != "" {
		entries = append(entries, Entry{Name: Workspace, Command: ws.Trust})
	}
	for _, p := range ws.Projects {
		if p.Trust != "" {
			entries = append(entries, ProjectEntry(p))
		}
	}
	return entries
}

// ProjectEntry returns the entry of p.
func ProjectEntry(p *workspace.Project) Entry {
	return Entry{Name: p.Name, Path: p.Path, Command: p.Trust}
}

// Record is the content of trust.json: the trust commands that last succeeded in the tree at Root.
type Record struct {
	Root      string            `json:"root"`
	Workspace string            `json:"workspace,omitempty"`
	Projects  map[string]string `json:"projects,omitempty"`
}

// Load returns the record of the tree at root. A missing, unreadable, or invalid file, or one recorded under
// another root, records nothing. Roots compare with symlinks resolved, so a tree reached through a link is the same.
func Load(root string) Record {
	var rec Record
	resolved := resolve(root)
	data, err := os.ReadFile(filePath(root))
	if err != nil || json.Unmarshal(data, &rec) != nil || rec.Root != resolved {
		return Record{Root: resolved}
	}
	return rec
}

// resolve returns root with symlinks resolved, or root when that fails.
func resolve(root string) string {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		return resolved
	}
	return root
}

// Pending returns the entries whose command the record does not hold.
func (r Record) Pending(entries []Entry) []Entry {
	return slices.DeleteFunc(slices.Clone(entries), func(e Entry) bool { return r.recorded(e) == e.Command })
}

func (r Record) recorded(e Entry) string {
	if e.Path == "" {
		return r.Workspace
	}
	return r.Projects[e.Path]
}

func (r *Record) set(e Entry) {
	if e.Path == "" {
		r.Workspace = e.Command
		return
	}
	if r.Projects == nil {
		r.Projects = make(map[string]string)
	}
	r.Projects[e.Path] = e.Command
}

// Save writes rec to the tree's trust.json under root with symlinks resolved, keeping only the projects at
// registered paths.
func Save(root string, rec Record, registered []string) error {
	kept := make(map[string]string)
	for p, cmd := range rec.Projects {
		if slices.Contains(registered, p) {
			kept[p] = cmd
		}
	}
	rec.Projects = kept
	rec.Root = resolve(root)
	var data bytes.Buffer
	enc := json.NewEncoder(&data)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rec); err != nil {
		return err
	}
	return workspace.WriteFileAtomic(filePath(root), data.Bytes())
}

// Run runs each entry's command as sh -c through ex, with no wrapper: in root for the workspace and in the project's
// directory otherwise. After each success it records the entry in rec and saves rec, keeping only the projects at
// registered paths. It stops at the first failure.
func Run(ctx context.Context, root string, rec *Record, entries []Entry, registered []string, ex runner.Executor,
	stdout, stderr io.Writer) error {
	for _, e := range entries {
		env := []string{"STEW_ROOT=" + root}
		if e.Path != "" {
			env = append(env, "STEW_PROJECT="+e.Name)
		}
		dir := filepath.Join(root, filepath.FromSlash(e.Path))
		if res := ex.Run(ctx, dir, env, []string{"sh", "-c", e.Command}, stdout, stderr); !res.OK() {
			return fmt.Errorf("trust %s: %s", e.Name, res.Cause())
		}
		rec.set(e)
		if err := Save(root, *rec, registered); err != nil {
			return fmt.Errorf("trust: save %s: %w", path.Join(workspace.DirName, File), err)
		}
	}
	return nil
}

func filePath(root string) string {
	return filepath.Join(root, workspace.DirName, File)
}
