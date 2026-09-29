package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/rknit/steward/internal/runner"
)

// CheckWrapper rejects a wrapper that cannot work: one without exactly one runner.StepPlaceholder, or one sh cannot
// parse once the placeholder is replaced by a path. It only parses the wrapper, with sh -n. "" means no wrapper.
func CheckWrapper(w string) error {
	if w == "" {
		return nil
	}
	if n := strings.Count(w, runner.StepPlaceholder); n != 1 {
		return fmt.Errorf("must contain %s exactly once (found %d)", runner.StepPlaceholder, n)
	}
	var stderr bytes.Buffer
	cmd := exec.Command("sh", "-n", "-c", strings.Replace(w, runner.StepPlaceholder, "/tmp/stew-check/step", 1))
	// bash as sh imports parse options such as extglob from BASHOPTS and SHELLOPTS in its environment.
	cmd.Env = []string{}
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}
