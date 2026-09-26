package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/rogpeppe/go-internal/testscript"
)

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"stew":     func() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) },
		"hupcount": hupcount,
	})
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

func TestScripts(t *testing.T) {
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
			env.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			env.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
			env.Setenv("GIT_AUTHOR_NAME", "stew test")
			env.Setenv("GIT_AUTHOR_EMAIL", "stew@example.com")
			env.Setenv("GIT_COMMITTER_NAME", "stew test")
			env.Setenv("GIT_COMMITTER_EMAIL", "stew@example.com")
			return nil
		},
	})
}
