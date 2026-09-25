package workspace

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// Workspace is a loaded and fully validated workspace.
type Workspace struct {
	Root     string
	Wrapper  string     // workspace wrapper from .stew/config.toml; "" means none
	Projects []*Project // topological order, ties broken by name
	byName   map[string]*Project
}

// Load reads the config, the registry, and every registered stew.toml, then validates names and dependencies.
func Load(root string) (*Workspace, error) {
	wrapper, err := LoadConfig(root)
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
	ws.Wrapper = wrapper
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
	for _, p := range projects {
		for _, dep := range p.Dependencies {
			manifest := path.Join(p.Path, ManifestFile)
			if dep == p.Name {
				return nil, fmt.Errorf("%s: project %q depends on itself", manifest, p.Name)
			}
			if _, ok := byName[dep]; !ok {
				return nil, fmt.Errorf("%s: project %q depends on unknown project %q", manifest, p.Name, dep)
			}
		}
	}
	if cycle := findCycle(projects, byName); cycle != nil {
		return nil, fmt.Errorf("dependency cycle: %s", strings.Join(cycle, " -> "))
	}
	return &Workspace{Root: root, Projects: topoSort(projects, byName), byName: byName}, nil
}

// Project returns the project with the given name.
func (w *Workspace) Project(name string) (*Project, bool) {
	p, ok := w.byName[name]
	return p, ok
}

// Select returns the named projects plus all their transitive dependencies, in topological order.
// named reports which returned projects were named. No names selects and names every project.
func (w *Workspace) Select(names []string) (selected []*Project, named map[string]bool, err error) {
	named = make(map[string]bool)
	if len(names) == 0 {
		for _, p := range w.Projects {
			named[p.Name] = true
		}
		return slices.Clone(w.Projects), named, nil
	}

	include := make(map[string]bool)
	var visit func(name string)
	visit = func(name string) {
		if include[name] {
			return
		}
		include[name] = true
		for _, dep := range w.byName[name].Dependencies {
			visit(dep)
		}
	}
	for _, name := range names {
		if _, ok := w.byName[name]; !ok {
			return nil, nil, fmt.Errorf("unknown project %q", name)
		}
		named[name] = true
		visit(name)
	}
	for _, p := range w.Projects {
		if include[p.Name] {
			selected = append(selected, p)
		}
	}
	return selected, named, nil
}

// topoSort orders projects so every dependency comes before its dependents.
// Among projects that are ready at the same time, the smallest name goes first.
func topoSort(projects []*Project, byName map[string]*Project) []*Project {
	pending := make(map[string]int, len(projects)) // name -> unmet dependency count
	dependents := make(map[string][]string)
	var ready []string
	for _, p := range projects {
		pending[p.Name] = len(p.Dependencies)
		for _, dep := range p.Dependencies {
			dependents[dep] = append(dependents[dep], p.Name)
		}
		if len(p.Dependencies) == 0 {
			ready = append(ready, p.Name)
		}
	}
	order := make([]*Project, 0, len(projects))
	for len(ready) > 0 {
		slices.Sort(ready)
		name := ready[0]
		ready = ready[1:]
		order = append(order, byName[name])
		for _, d := range dependents[name] {
			pending[d]--
			if pending[d] == 0 {
				ready = append(ready, d)
			}
		}
	}
	return order
}

// findCycle returns one dependency cycle as a list of names whose last element repeats the first, or nil.
func findCycle(projects []*Project, byName map[string]*Project) []string {
	const (
		unvisited = iota
		inStack
		done
	)
	state := make(map[string]int, len(projects))
	var stack []string
	var cycle []string

	var visit func(name string) bool
	visit = func(name string) bool {
		state[name] = inStack
		stack = append(stack, name)
		for _, dep := range byName[name].Dependencies {
			switch state[dep] {
			case inStack:
				start := slices.Index(stack, dep)
				cycle = append(slices.Clone(stack[start:]), dep)
				return true
			case unvisited:
				if visit(dep) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = done
		return false
	}

	names := make([]string, 0, len(projects))
	for _, p := range projects {
		names = append(names, p.Name)
	}
	slices.Sort(names)
	for _, name := range names {
		if state[name] == unvisited && visit(name) {
			return cycle
		}
	}
	return nil
}
