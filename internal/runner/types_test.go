package runner

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestLogErrorOutcome(t *testing.T) {
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
