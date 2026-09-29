package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// ConfigFile is the workspace config's file name inside .stew/.
const ConfigFile = "config.toml"

// ConfigPath returns the path of the workspace config for a workspace root.
func ConfigPath(root string) string {
	return filepath.Join(root, DirName, ConfigFile)
}

// ConfigTemplate is the config.toml that stew init writes.
const ConfigTemplate = `# Wraps every command stew runs. {{STEW_STEP}} marks where the command goes. "" means none.
workspace_wrapper = ""
# Makes the wrapper usable in a new tree, e.g. "direnv allow .". Runs once per tree, with consent. "" means none.
workspace_trust = ""
# "parallel" runs independent sections at the same time; "serial" runs one at a time. Default "parallel".
# concurrency = "parallel"
# Most sections running at once in parallel mode. Default: the number of CPUs. stew run -j overrides it.
# jobs = 8
`

// Config is the checked content of .stew/config.toml.
type Config struct {
	Wrapper string // workspace wrapper; "" means none
	Trust   string // workspace trust command; "" means none
	Serial  bool   // concurrency = "serial": one section at a time
	Jobs    int    // most sections running at once; 0 when unset
}

type rawConfig struct {
	WorkspaceWrapper string `toml:"workspace_wrapper"`
	WorkspaceTrust   string `toml:"workspace_trust"`
	Concurrency      string `toml:"concurrency"`
	Jobs             int    `toml:"jobs"`
}

// LoadConfig reads and checks .stew/config.toml.
func LoadConfig(root string) (Config, error) {
	file := ConfigPath(root)
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, fmt.Errorf("%s: missing", file)
	}
	if err != nil {
		return Config{}, err
	}
	var raw rawConfig
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", file, err)
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		return Config{}, fmt.Errorf("%s: unknown key %q", file, keys[0].String())
	}
	for _, key := range []string{"workspace_wrapper", "workspace_trust"} {
		if !md.IsDefined(key) {
			return Config{}, fmt.Errorf("%s: missing key %q", file, key)
		}
	}
	if err := CheckWrapper(raw.WorkspaceWrapper); err != nil {
		return Config{}, fmt.Errorf("%s: workspace_wrapper: %w", file, err)
	}
	if md.IsDefined("concurrency") && raw.Concurrency != "parallel" && raw.Concurrency != "serial" {
		return Config{}, fmt.Errorf(`%s: concurrency: must be "parallel" or "serial"`, file)
	}
	if md.IsDefined("jobs") && raw.Jobs < 1 {
		return Config{}, fmt.Errorf("%s: jobs: must be at least 1", file)
	}
	return Config{Wrapper: raw.WorkspaceWrapper, Trust: raw.WorkspaceTrust, Serial: raw.Concurrency == "serial",
		Jobs: raw.Jobs}, nil
}
