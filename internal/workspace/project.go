package workspace

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// ManifestFile is the per-project config file name.
const ManifestFile = "stew.toml"

// Section holds one section's commands and requirements. An empty command means "none".
type Section struct {
	Run      string
	SkipIf   string
	Verify   string
	Requires []Key
}

// Project is one parsed and validated stew.toml.
type Project struct {
	Name     string
	Path     string // root-relative, slash-separated, as registered
	Wrapper  string // project wrapper, nested inside the workspace wrapper; "" means none
	Trust    string // project trust command; "" means none
	Sections map[string]*Section
}

// SectionNames returns the project's section names, sorted.
func (p *Project) SectionNames() []string {
	return slices.Sorted(maps.Keys(p.Sections))
}

// Dependencies returns the other projects named in any of the project's requires, sorted.
func (p *Project) Dependencies() []string {
	deps := make(map[string]bool)
	for _, s := range p.Sections {
		for _, k := range s.Requires {
			if k.Project != p.Name {
				deps[k.Project] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(deps))
}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ValidName reports whether name is a valid project name.
func ValidName(name string) bool {
	return nameRE.MatchString(name)
}

var sectionKeys = []string{"run", "skip_if", "verify", "requires"}

// ParseProject parses stew.toml content. file is used only in error messages.
func ParseProject(file string, data []byte) (*Project, error) {
	var raw map[string]any
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, fmt.Errorf("%s: %v", file, err)
	}
	p, err := parseProject(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return p, nil
}

func parseProject(raw map[string]any) (*Project, error) {
	for _, key := range []string{"name", "project_wrapper", "project_trust"} {
		if _, ok := raw[key]; !ok {
			return nil, fmt.Errorf("missing key %q", key)
		}
	}
	name, ok := raw["name"].(string)
	if !ok {
		return nil, fmt.Errorf("name: want a string")
	}
	wrapper, ok := raw["project_wrapper"].(string)
	if !ok {
		return nil, fmt.Errorf("project_wrapper: want a string")
	}
	trust, ok := raw["project_trust"].(string)
	if !ok {
		return nil, fmt.Errorf("project_trust: want a string")
	}
	if !ValidName(name) {
		return nil, fmt.Errorf("invalid name %q (want [a-z0-9][a-z0-9._-]*)", name)
	}
	if err := CheckWrapper(wrapper); err != nil {
		return nil, fmt.Errorf("project_wrapper: %v", err)
	}

	p := &Project{Name: name, Wrapper: wrapper, Trust: trust, Sections: make(map[string]*Section)}
	for _, key := range slices.Sorted(maps.Keys(raw)) {
		if key == "name" || key == "project_wrapper" || key == "project_trust" {
			continue
		}
		if err := p.parseTable([]string{key}, raw[key]); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// parseTable reads the TOML value at path: a section if it holds a section key, otherwise a namespace of tables.
func (p *Project) parseTable(path []string, v any) error {
	table, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("unknown key %q", strings.Join(path, "."))
	}
	if seg := path[len(path)-1]; !segmentRE.MatchString(seg) {
		return fmt.Errorf("invalid section name segment %q (want [a-z0-9][a-z0-9_-]*)", seg)
	}
	if !slices.ContainsFunc(sectionKeys, func(k string) bool { _, ok := table[k]; return ok }) {
		for _, key := range slices.Sorted(maps.Keys(table)) {
			if err := p.parseTable(append(slices.Clone(path), key), table[key]); err != nil {
				return err
			}
		}
		return nil
	}

	name := strings.Join(path, ".")
	s, err := p.parseSection(name, table)
	if err != nil {
		return err
	}
	p.Sections[name] = s
	return nil
}

func (p *Project) parseSection(name string, table map[string]any) (*Section, error) {
	if _, ok := table["run"]; !ok {
		return nil, fmt.Errorf("[%s]: missing key %q", name, "run")
	}
	s := &Section{}
	for _, key := range slices.Sorted(maps.Keys(table)) {
		v := table[key]
		switch key {
		case "run", "skip_if", "verify":
			cmd, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("[%s]: %s: want a string", name, key)
			}
			switch key {
			case "run":
				s.Run = cmd
			case "skip_if":
				s.SkipIf = cmd
			case "verify":
				s.Verify = cmd
			}
		case "requires":
			requires, err := p.parseRequires(name, v)
			if err != nil {
				return nil, err
			}
			s.Requires = requires
		default:
			if _, isTable := v.(map[string]any); isTable {
				return nil, fmt.Errorf("[%s]: a section cannot contain sections", name)
			}
			return nil, fmt.Errorf("[%s]: unknown key %q", name, key)
		}
	}
	return s, nil
}

func (p *Project) parseRequires(name string, v any) ([]Key, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("[%s]: requires: want a list of strings", name)
	}
	self := Key{Project: p.Name, Section: name}
	var keys []Key
	for _, item := range list {
		entry, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("[%s]: requires: want a list of strings", name)
		}
		k, ok := ParseKey(entry)
		switch {
		case !ok:
			return nil, fmt.Errorf("[%s]: requires %q: want <project>:<section>", name, entry)
		case k == self:
			return nil, fmt.Errorf("[%s]: requires %q: section requires itself", name, entry)
		case slices.Contains(keys, k):
			return nil, fmt.Errorf("[%s]: duplicate requires %q", name, entry)
		}
		keys = append(keys, k)
	}
	return keys, nil
}

// LoadProject reads and parses <root>/<rel>/stew.toml.
func LoadProject(root, rel string) (*Project, error) {
	file := filepath.Join(root, filepath.FromSlash(rel), ManifestFile)
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("project %q: missing %s", rel, ManifestFile)
		}
		return nil, err
	}
	p, err := ParseProject(file, data)
	if err != nil {
		return nil, err
	}
	p.Path = rel
	return p, nil
}

// templateLines is the stew.toml that `stew add` writes; %s is the project name.
var templateLines = []string{
	`name = "%s"`,
	`# Wraps every command of this project, inside the workspace wrapper.`,
	`# {{STEW_STEP}} marks where the command goes. "" means none.`,
	`project_wrapper = ""`,
	`# Makes the project wrapper usable in a new tree, e.g. "mise trust". Runs once per tree, with consent.`,
	`# "" means none.`,
	`project_trust = ""`,
	``,
	"# Sections: any [name] with a `run` key. `stew run '<regex>'` runs sections whose",
	"# <project>:<section> key matches; `stew build` is `stew run '.*:build'`.",
	`# skip_if exit 0 skips the section. verify runs after run and must exit 0.`,
	`# requires lists sections that must succeed first, as "<project>:<section>".`,
	`#`,
	`# [build]`,
	`# run = ""`,
	`# skip_if = ""`,
	`# verify = ""`,
	`# requires = []`,
}

// Template returns the stew.toml content that `stew add` writes. name must be valid.
func Template(name string) []byte {
	return fmt.Appendf(nil, strings.Join(templateLines, "\n")+"\n", name)
}
