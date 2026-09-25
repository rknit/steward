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
`

type rawConfig struct {
	WorkspaceWrapper string `toml:"workspace_wrapper"`
}

// LoadConfig reads .stew/config.toml and returns the checked workspace wrapper.
func LoadConfig(root string) (string, error) {
	file := ConfigPath(root)
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%s: missing", file)
	}
	if err != nil {
		return "", err
	}
	var raw rawConfig
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		return "", fmt.Errorf("%s: %w", file, err)
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		return "", fmt.Errorf("%s: unknown key %q", file, keys[0].String())
	}
	if !md.IsDefined("workspace_wrapper") {
		return "", fmt.Errorf("%s: missing key %q", file, "workspace_wrapper")
	}
	if err := CheckWrapper(raw.WorkspaceWrapper); err != nil {
		return "", fmt.Errorf("%s: workspace_wrapper: %w", file, err)
	}
	return raw.WorkspaceWrapper, nil
}
