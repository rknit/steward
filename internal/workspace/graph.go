package workspace

import (
	"container/heap"
	"fmt"
	"path"
	"slices"
	"strings"
)

// Workspace is a loaded and fully validated workspace.
type Workspace struct {
	Root     string
	Wrapper  string     // workspace wrapper from .stew/config.toml; "" means none
	Trust    string     // workspace trust command from .stew/config.toml; "" means none
	Serial   bool       // concurrency = "serial" in .stew/config.toml
	Jobs     int        // jobs from .stew/config.toml; 0 when unset
	Projects []*Project // sorted by name
	byName   map[string]*Project
}

// Load reads the config, the registry, and every registered stew.toml, then validates names and requires.
func Load(root string) (*Workspace, error) {
	cfg, err := LoadConfig(root)
	if err != nil {
		return nil, err
	}
	paths, err := LoadRegistry(root)
	if err != nil {
		return nil, err
	}
	projects := make([]*Project, 0, len(paths))
	for _, rel := range paths {
		p, err := LoadProject(root, rel)
		if err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	ws, err := newWorkspace(root, projects)
	if err != nil {
		return nil, err
	}
	ws.Wrapper, ws.Trust, ws.Serial, ws.Jobs = cfg.Wrapper, cfg.Trust, cfg.Serial, cfg.Jobs
	return ws, nil
}

func newWorkspace(root string, projects []*Project) (*Workspace, error) {
	byName := make(map[string]*Project, len(projects))
	for _, p := range projects {
		if other, ok := byName[p.Name]; ok {
			return nil, fmt.Errorf("duplicate project name %q (%s and %s)", p.Name, other.Path, p.Path)
		}
		byName[p.Name] = p
	}
	ws := &Workspace{
		Root:     root,
		Projects: slices.SortedFunc(slices.Values(projects), func(a, b *Project) int { return strings.Compare(a.Name, b.Name) }),
		byName:   byName,
	}
	for _, p := range ws.Projects {
		if err := checkRequires(p, ws.Project, ""); err != nil {
			return nil, err
		}
	}
	if cycle := ws.findCycle(); cycle != nil {
		return nil, fmt.Errorf("cycle: %s", strings.Join(cycle, " -> "))
	}
	return ws, nil
}

// checkRequires reports the first requires entry of p that names no section. hint follows an unknown project error.
func checkRequires(p *Project, lookup func(string) (*Project, bool), hint string) error {
	manifest := path.Join(p.Path, ManifestFile)
	for _, name := range p.SectionNames() {
		for _, k := range p.Sections[name].Requires {
			target, ok := lookup(k.Project)
			switch {
			case !ok:
				return fmt.Errorf("%s: [%s]: requires %q: unknown project %q%s", manifest, name, k, k.Project, hint)
			case target.Sections[k.Section] == nil:
				return fmt.Errorf("%s: [%s]: requires %q: %s has no section %q", manifest, name, k, k.Project, k.Section)
			}
		}
	}
	return nil
}

// CheckNewProject checks the requires of p, a project not yet registered, against w and p itself.
// A new project cannot be required by registered ones, so a cycle can only run through its own sections.
func (w *Workspace) CheckNewProject(p *Project) error {
	lookup := func(name string) (*Project, bool) {
		if name == p.Name {
			return p, true
		}
		return w.Project(name)
	}
	if err := checkRequires(p, lookup, "; add it first"); err != nil {
		return err
	}
	own := &Project{Name: p.Name, Path: p.Path, Sections: make(map[string]*Section, len(p.Sections))}
	for name, s := range p.Sections {
		requires := slices.DeleteFunc(slices.Clone(s.Requires), func(k Key) bool { return k.Project != p.Name })
		own.Sections[name] = &Section{Requires: requires}
	}
	alone := &Workspace{Projects: []*Project{own}, byName: map[string]*Project{own.Name: own}}
	if cycle := alone.findCycle(); cycle != nil {
		return fmt.Errorf("%s: cycle: %s", path.Join(p.Path, ManifestFile), strings.Join(cycle, " -> "))
	}
	return nil
}

// Project returns the project with the given name.
func (w *Workspace) Project(name string) (*Project, bool) {
	p, ok := w.byName[name]
	return p, ok
}

func (w *Workspace) section(k Key) *Section {
	return w.byName[k.Project].Sections[k.Section]
}

// keys returns every section key, sorted by project name, then section name.
func (w *Workspace) keys() []Key {
	var keys []Key
	for _, p := range w.Projects {
		for _, name := range p.SectionNames() {
			keys = append(keys, Key{Project: p.Name, Section: name})
		}
	}
	return keys
}

// Node is one selected section.
type Node struct {
	Key     Key
	Project *Project
	Section *Section
	Matched bool // a pattern matched it; otherwise requires pulled it in
}

// Select returns the sections whose key matches a pattern, plus their requires transitively, in execution order:
// topological, with ready ties broken by project name, then section name. Every pattern must match a section.
func (w *Workspace) Select(patterns []string) ([]Node, error) {
	all := w.keys()
	matched := make(map[Key]bool)
	for _, p := range patterns {
		re, err := CompileKeyPattern(p)
		if err != nil {
			return nil, err
		}
		found := false
		for _, k := range all {
			if re.MatchString(k.String()) {
				matched[k] = true
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf(`pattern "%s" matches no section`, p)
		}
	}

	include := make(map[Key]bool)
	var visit func(Key)
	visit = func(k Key) {
		if include[k] {
			return
		}
		include[k] = true
		for _, req := range w.section(k).Requires {
			visit(req)
		}
	}
	for k := range matched {
		visit(k)
	}

	pending := make(map[Key]int)
	dependents := make(map[Key][]Key)
	ready := &keyHeap{}
	for _, k := range all {
		if !include[k] {
			continue
		}
		reqs := w.section(k).Requires
		pending[k] = len(reqs)
		for _, req := range reqs {
			dependents[req] = append(dependents[req], k)
		}
		if len(reqs) == 0 {
			heap.Push(ready, k)
		}
	}
	order := make([]Node, 0, len(include))
	for ready.Len() > 0 {
		k := heap.Pop(ready).(Key)
		order = append(order, Node{Key: k, Project: w.byName[k.Project], Section: w.section(k), Matched: matched[k]})
		for _, d := range dependents[k] {
			pending[d]--
			if pending[d] == 0 {
				heap.Push(ready, d)
			}
		}
	}
	return order, nil
}

// keyHeap is a min-heap of keys ordered by project name, then section name.
type keyHeap []Key

func (h keyHeap) Len() int           { return len(h) }
func (h keyHeap) Less(i, j int) bool { return compareKeys(h[i], h[j]) < 0 }
func (h keyHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *keyHeap) Push(x any)        { *h = append(*h, x.(Key)) }

func (h *keyHeap) Pop() any {
	old := *h
	k := old[len(old)-1]
	*h = old[:len(old)-1]
	return k
}

// findCycle returns one requires cycle as keys whose last element repeats the first, or nil.
func (w *Workspace) findCycle() []string {
	const (
		unvisited = iota
		inStack
		done
	)
	state := make(map[Key]int)
	var stack []Key
	var cycle []string

	var visit func(k Key) bool
	visit = func(k Key) bool {
		state[k] = inStack
		stack = append(stack, k)
		for _, req := range w.section(k).Requires {
			switch state[req] {
			case inStack:
				for _, s := range stack[slices.Index(stack, req):] {
					cycle = append(cycle, s.String())
				}
				cycle = append(cycle, req.String())
				return true
			case unvisited:
				if visit(req) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[k] = done
		return false
	}

	for _, k := range w.keys() {
		if state[k] == unvisited && visit(k) {
			return cycle
		}
	}
	return nil
}
