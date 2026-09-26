package runner

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestResultWrapped(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		res               Result
		ok, wrapperFailed bool
		want              string
	}{
		{Result{Wrapped: true, Reaches: 0}, false, true, "wrapper did not run the command (exit 0)"},
		{Result{Wrapped: true, Reaches: 2, ExitCode: 1}, false, true, "wrapper ran the command 2 times (exit 1)"},
		{Result{Wrapped: true, Reaches: 0, Signal: "SIGKILL"}, false, true, "wrapper did not run the command (signal SIGKILL)"},
		{Result{Wrapped: true, Reaches: 0, Err: errors.New("no sh")}, false, true, "wrapper did not run the command (cannot start: no sh)"},
		{Result{Wrapped: true, Reaches: 1, ExitCode: 3}, false, false, "exit 3"},
		{Result{Wrapped: true, Reaches: 1}, true, false, "exit 0"},
		{Result{Wrapped: true, Reaches: 1, Unfinished: true}, false, true, "wrapper exited before the command finished (exit 0)"},
		{Result{Wrapped: true, Reaches: 1, Unfinished: true, Signal: "SIGTERM"}, false, true,
			"wrapper exited before the command finished (signal SIGTERM)"},
		{Result{}, true, false, "exit 0"},
	} {
		if tc.res.OK() != tc.ok || tc.res.WrapperFailed() != tc.wrapperFailed {
			t.Errorf("%+v: OK() = %v, WrapperFailed() = %v", tc.res, tc.res.OK(), tc.res.WrapperFailed())
		}
		if got := tc.res.Cause(); got != tc.want {
			t.Errorf("Cause() = %q, want %q", got, tc.want)
		}
	}
}

func TestSectionKey(t *testing.T) {
	t.Parallel()
	if k := (Section{Project: "my.lib", Name: "ci.full"}).Key(); k != "my.lib:ci.full" {
		t.Errorf("Key = %q", k)
	}
}

func TestLogErrorOutcome(t *testing.T) {
	t.Parallel()
	err := errors.New("boom")
	tests := map[string]struct {
		in   Outcome
		want Outcome
	}{
		"done becomes fail": {
			in:   Outcome{Status: Done, Duration: 5 * time.Millisecond},
			want: Outcome{Status: Fail, Duration: 5 * time.Millisecond, Cause: "log error: boom"},
		},
		"fail is unchanged": {
			in:   Outcome{Status: Fail, Duration: 3 * time.Millisecond, Cause: "exit 1"},
			want: Outcome{Status: Fail, Duration: 3 * time.Millisecond, Cause: "exit 1"},
		},
		"interrupted is unchanged": {
			in:   Outcome{Status: Interrupted, Duration: time.Second, Cause: "signal SIGINT"},
			want: Outcome{Status: Interrupted, Duration: time.Second, Cause: "signal SIGINT"},
		},
	}
	for name, tt := range tests {
		if got := LogErrorOutcome(tt.in, err); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: LogErrorOutcome = %+v, want %+v", name, got, tt.want)
		}
	}
}
