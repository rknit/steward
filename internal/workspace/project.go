package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// ManifestFile is the per-project config file name.
const ManifestFile = "stew.toml"

// Level is a CI level name.
type Level string

// CI levels.
const (
	LevelPreCommit Level = "pre-commit"
	LevelPrePush   Level = "pre-push"
	LevelQuick     Level = "quick"
	LevelFull      Level = "full"
)

// fallback maps each level to the level used when a project does not define it. LevelFull has none.
var fallback = map[Level]Level{
	LevelPreCommit: LevelQuick,
	LevelPrePush:   LevelFull,
	LevelQuick:     LevelFull,
}

// ParseLevel returns the Level named s, or an error for an unknown name.
func ParseLevel(s string) (Level, error) {
	l := Level(s)
	if _, ok := fallback[l]; ok || l == LevelFull {
		return l, nil
	}
	return "", fmt.Errorf("unknown CI level %q (want full, quick, pre-commit, or pre-push)", s)
}

// Phase holds a setup or build phase's commands. An empty string means "no command".
type Phase struct {
	Run    string
	Verify string
}

// Project is one parsed and validated stew.toml.
type Project struct {
	Name         string
	Path         string // root-relative, slash-separated, as registered
	Dependencies []string
	Wrapper      string // project wrapper, nested inside the workspace wrapper; "" means none
	Setup        Phase
	Build        Phase
	CI           map[Level]string // run command per defined level; LevelFull is always present
}

// ResolveCI walks the fallback chain from the requested level and returns the first level the project defines.
func (p *Project) ResolveCI(requested Level) (used Level, run string) {
	for l := requested; ; l = fallback[l] {
		if cmd, ok := p.CI[l]; ok {
			return l, cmd
		}
		if l == LevelFull {
			panic("workspace: project without ci.full: " + p.Name)
		}
	}
}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ValidName reports whether name is a valid project name.
func ValidName(name string) bool {
	return nameRE.MatchString(name)
}

type rawPhase struct {
	Run    string `toml:"run"`
	Verify string `toml:"verify"`
}

type rawCI struct {
	Run string `toml:"run"`
}

type rawProject struct {
	Name         string           `toml:"name"`
	Dependencies []string         `toml:"dependencies"`
	Wrapper      string           `toml:"project_wrapper"`
	Setup        rawPhase         `toml:"setup"`
	Build        rawPhase         `toml:"build"`
	CI           map[string]rawCI `toml:"ci"`
}

// ParseProject parses stew.toml content. file is used only in error messages.
func ParseProject(file string, data []byte) (*Project, error) {
	errf := func(format string, args ...any) error {
		return fmt.Errorf("%s: "+format, append([]any{file}, args...)...)
	}

	var raw rawProject
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		return nil, errf("%v", err)
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		return nil, errf("unknown key %q", keys[0].String())
	}

	required := [][]string{
		{"name"}, {"dependencies"}, {"project_wrapper"},
		{"setup"}, {"setup", "run"}, {"setup", "verify"},
		{"build"}, {"build", "run"}, {"build", "verify"},
		{"ci", "full"},
	}
	for _, key := range required {
		if !md.IsDefined(key...) {
			return nil, errf("missing %s", describeKey(key))
		}
	}

	ci := make(map[Level]string, len(raw.CI))
	for name, section := range raw.CI {
		level, err := ParseLevel(name)
		if err != nil {
			return nil, errf("%v", err)
		}
		if !md.IsDefined("ci", name, "run") {
			return nil, errf("missing key %q", "ci."+name+".run")
		}
		ci[level] = section.Run
	}

	if !ValidName(raw.Name) {
		return nil, errf("invalid name %q (want [a-z0-9][a-z0-9._-]*)", raw.Name)
	}
	seen := make(map[string]bool, len(raw.Dependencies))
	for _, dep := range raw.Dependencies {
		if seen[dep] {
			return nil, errf("duplicate dependency %q", dep)
		}
		seen[dep] = true
	}
	if err := CheckWrapper(raw.Wrapper); err != nil {
		return nil, errf("project_wrapper: %v", err)
	}

	return &Project{
		Name:         raw.Name,
		Dependencies: raw.Dependencies,
		Wrapper:      raw.Wrapper,
		Setup:        Phase(raw.Setup),
		Build:        Phase(raw.Build),
		CI:           ci,
	}, nil
}

func describeKey(key []string) string {
	joined := strings.Join(key, ".")
	switch joined {
	case "setup", "build", "ci.full":
		return "section [" + joined + "]"
	}
	return fmt.Sprintf("key %q", joined)
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
	`dependencies = []`,
	`# Wraps every command of this project, inside the workspace wrapper.`,
	`# {{STEW_STEP}} marks where the command goes. "" means none.`,
	`project_wrapper = ""`,
	``,
	"# Each phase: `verify` runs first; exit 0 skips `run`.",
	"# Otherwise `run` runs, then `verify` confirms. Empty strings are no-ops.",
	`[setup]`,
	`run = ""`,
	`verify = ""`,
	``,
	`[build]`,
	`run = ""`,
	`verify = ""`,
	``,
	`# Levels: pre-commit falls back to quick; quick and pre-push fall back to full.`,
	`[ci.full]`,
	`run = ""`,
}

// Template returns the stew.toml content that `stew add` writes. name must be valid.
func Template(name string) []byte {
	return fmt.Appendf(nil, strings.Join(templateLines, "\n")+"\n", name)
}
