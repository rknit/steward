package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/rogpeppe/go-internal/testscript"
)

// TestMain runs this test binary as stew when it is invoked as "stew", as test scripts and git hooks do with the
// stew on PATH.
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "stew" {
		main()
	}
	testscript.Main(m, map[string]func(){"hupcount": hupcount})
}

// hupcount appends a line to the file $1 for each SIGHUP it gets, for 3 seconds. It prints "hupcount ready" once it
// counts.
func hupcount() {
	hups := make(chan os.Signal, 16)
	signal.Notify(hups, syscall.SIGHUP)
	f, err := os.OpenFile(os.Args[1], os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("hupcount ready")
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-hups:
			fmt.Fprintln(f, "hup")
		case <-deadline:
			os.Exit(0)
		}
	}
}

type (
	scriptVarsKey struct{}
	statusKey     struct{}
)

func TestScripts(t *testing.T) {
	bin := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, filepath.Join(bin, "stew")); err != nil {
		t.Fatal(err)
	}
	testscript.Run(t, testscript.Params{
		Dir:                 "testdata/script",
		RequireExplicitExec: true,
		Condition: func(cond string) (bool, error) {
			if cond == "root" {
				return os.Geteuid() == 0, nil
			}
			return false, fmt.Errorf("unknown condition %q", cond)
		},
		Setup: func(env *testscript.Env) error {
			env.Setenv("PATH", bin+string(os.PathListSeparator)+env.Getenv("PATH"))
			env.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			env.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
			env.Setenv("GIT_AUTHOR_NAME", "stew test")
			env.Setenv("GIT_AUTHOR_EMAIL", "stew@example.com")
			env.Setenv("GIT_COMMITTER_NAME", "stew test")
			env.Setenv("GIT_COMMITTER_EMAIL", "stew@example.com")
			var vars []string
			for _, kv := range env.Vars {
				key, _, _ := strings.Cut(kv, "=")
				vars = append(vars, key)
			}
			env.Values[scriptVarsKey{}] = vars
			env.Values[statusKey{}] = new(int)
			return nil
		},
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			"stew":   stewCmd,
			"status": statusCmd,
		},
	})
}

// stewCmd runs stew in this test process, in the script's directory. Its environment has only the variables the
// script started with, at their current values, because testscript does not list the others; a script that adds
// a variable for stew to see uses exec stew. stew exec always needs exec stew exec.
func stewCmd(ts *testscript.TestScript, neg bool, args []string) {
	if len(args) > 0 && args[0] == "exec" {
		ts.Fatalf("stew exec moves the terminal and handles signals for the whole process; use exec stew exec")
	}
	var env []string
	for _, key := range ts.Value(scriptVarsKey{}).([]string) {
		env = append(env, key+"="+ts.Getenv(key))
	}
	loc, err := time.LoadLocation(ts.Getenv("TZ"))
	ts.Check(err)
	proc := process{dir: ts.MkAbs("."), env: env, loc: loc, stdout: ts.Stdout(), stderr: ts.Stderr()}
	code := run(proc, args)
	*ts.Value(statusKey{}).(*int) = code
	if code != 0 && !neg {
		ts.Fatalf("stew exited %d", code)
	}
	if code == 0 && neg {
		ts.Fatalf("stew succeeded unexpectedly")
	}
}

// statusCmd checks the exit code of the script's last stew.
func statusCmd(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 1 {
		ts.Fatalf("usage: status code")
	}
	want, err := strconv.Atoi(args[0])
	ts.Check(err)
	if got := *ts.Value(statusKey{}).(*int); got != want {
		ts.Fatalf("stew exited %d, want %d", got, want)
	}
}
