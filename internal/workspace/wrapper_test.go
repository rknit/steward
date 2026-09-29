package workspace

import (
	"strings"
	"testing"
)

func TestCheckWrapperAccepts(t *testing.T) {
	t.Parallel()
	for _, w := range []string{
		"",
		"tool exec . {{STEW_STEP}}",
		". ./env.sh && {{STEW_STEP}}",
		`tool exec "$STEW_ROOT" {{STEW_STEP}}`,
		"tool --run '{{STEW_STEP}}'",
		`tool -c ". ./env.sh && {{STEW_STEP}}"`,
		"set -x\ntool exec . {{STEW_STEP}}",
		"tool exec . {{STEW_STEP}}\n",
		"tool # {{STEW_STEP}}",
		"false; {{STEW_STEP}}",
	} {
		if err := CheckWrapper(w); err != nil {
			t.Errorf("CheckWrapper(%q) = %v", w, err)
		}
	}
}

func TestCheckWrapperRejects(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ wrapper, want string }{
		{"tool exec .", "must contain {{STEW_STEP}} exactly once (found 0)"},
		{"{{STEW_STEP}}; {{STEW_STEP}}", "must contain {{STEW_STEP}} exactly once (found 2)"},
		{"{{STEW_STEP", "must contain {{STEW_STEP}} exactly once (found 0)"},
	} {
		err := CheckWrapper(tc.wrapper)
		if err == nil || err.Error() != tc.want {
			t.Errorf("CheckWrapper(%q) = %v, want %q", tc.wrapper, err, tc.want)
		}
	}
	// The rest of the message is the shell's own.
	for _, wrapper := range []string{`tool "unbalanced {{STEW_STEP}}`, "if true {{STEW_STEP}}"} {
		if err := CheckWrapper(wrapper); err == nil || !strings.HasPrefix(err.Error(), "sh: ") {
			t.Errorf("CheckWrapper(%q) = %v, want prefix %q", wrapper, err, "sh: ")
		}
	}
}
