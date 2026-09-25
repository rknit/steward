package main

import (
	"fmt"
	"os"
	"testing"
	_ "time/tzdata"

	"github.com/rogpeppe/go-internal/testscript"
)

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"stew": func() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) },
	})
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
