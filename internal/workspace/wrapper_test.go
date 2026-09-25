package workspace

import (
	"strings"
	"testing"
)

func TestCheckWrapperAccepts(t *testing.T) {
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
	for _, tc := range []struct{ wrapper, want string }{
		{"tool exec .", "must contain {{STEW_STEP}} exactly once (found 0)"},
		{"{{STEW_STEP}}; {{STEW_STEP}}", "must contain {{STEW_STEP}} exactly once (found 2)"},
		{"{{STEW_STEP", "must contain {{STEW_STEP}} exactly once (found 0)"},
		{`tool "unbalanced {{STEW_STEP}}`, "sh: "},
		{"if true {{STEW_STEP}}", "sh: "},
	} {
		err := CheckWrapper(tc.wrapper)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("CheckWrapper(%q) = %v, want %q", tc.wrapper, err, tc.want)
		}
	}
}
