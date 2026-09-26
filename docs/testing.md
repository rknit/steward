# Testing

How tests are written, verified, and run in this repository.

## Principles

- Test behavior and contracts, not implementation details.
- Every test is deterministic: same result on every run, under any load, in
  any order, alone or in parallel.
- Every test is fast. Time goes to work under test, never to waiting.
- Every test is hermetic: it reads nothing from the developer's machine or the
  process running it that it did not set up itself.
- A test that cannot fail is not a test. Prove each new test fails when the
  behavior it covers breaks (see Verifying a Test).

## Waiting

Never wait a fixed time. Wait for the event that makes the outcome certain.

| Need | Do | Don't |
| --- | --- | --- |
| Something to happen | Poll for it every 20-50 ms, with a 10 s deadline | `sleep 1`, `time.Sleep` |
| Something not to happen | Assert it at the moment the contract guarantees it | Wait N ms, then check it didn't happen |
| No second event | Send an ordered marker through the same path, then count | Hold a window open with `sleep` |
| A process to be gone | Check once the code under test returns, if it guarantees that | Poll until it is gone |

- Bound every loop, in Go and in shell, so a regression fails the test instead
  of hanging the suite: `i=0; while [ ! -f started ] && [ $i -lt 200 ]; do
  sleep 0.05; i=$((i+1)); done`.
- After a bounded poll, assert the event happened (`exists core/started`). A
  poll that times out silently lets the rest of the test pass for the wrong
  reason.
- A negative check needs a guarantee behind it. stew stops a step's process
  group before `stew build` returns, so `kill -0` right after it returns proves
  the process is gone. Without such a guarantee, find the event that makes the
  outcome final instead of waiting.
- An ordered marker works when one sender passes signals on in order. stew
  forwards signals from one channel, so a SIGTERM sent through stew after a
  SIGINT reaches the command after any duplicate SIGINT. The command traps the
  marker and records that it arrived; the test waits for that record, then
  counts.
- Upper bounds on elapsed time (`took >= 2*time.Second`) are fine as
  assertions. They are not waits.

## Processes

A `go test -race` binary sleeps 1 s when it exits with status 0
(`runtime/proc.go`, `racefini`). Every successful `stew` subprocess costs 1 s
under `-race`. Do not hide this with `GORACE`; avoid the subprocess.

- In test scripts, `stew` runs in-process (`stewCmd` in
  `cmd/stew/main_test.go`). Use it by default.
- Use `exec stew` only when the test needs a real process:
  - signals sent to stew,
  - `stew exec`, which moves the terminal and handles signals for the whole
    process,
  - stdin, which in-process `stew` does not take,
  - git hooks, which run the `stew` on `PATH`,
  - a variable the script adds that in-process `stew` does not pass (only the
    script's starting variables and stew's run variables are passed),
  - stew running in the background (`exec stew ... &`) while the script does
    other work.
- Check exit codes of in-process `stew` with `! stew ...` and `status N`, not
  `exec sh -c 'stew ...; echo "exit=$?"'`. `status` sees only in-process
  `stew`; for a real process, `sh -c '...; echo "exit=$?"'` is the way to
  assert the exact code.
- Capture output with `cp stdout out`, not `sh -c 'stew ... > out'`.
- Merge checks of the same scenario into one invocation. A process that exits
  non-zero does not pay the race sleep, so fold a success check into a call
  that already exits non-zero when both check the same scenario.
- Split a script by topic when its subprocesses are the suite's critical path.
  Scripts run in parallel; commands inside one script do not.

## Process-Global State

Parallel tests share one process. Anything process-wide is shared.

- Code under test takes its working directory, environment, time zone, output,
  and temp directory as values: `process` in `cmd/stew`, `Shell.Environ`,
  `NewStepDir(tmp)`, `githook.Install(..., env)`. Do not read `os.Getwd`,
  `os.Environ`, `os.Getenv`, `os.TempDir`, or `time.Local` below `main`.
- No `t.Setenv`, `os.Setenv`, `os.Unsetenv`, or `os.Chdir` in tests. Build the
  environment the code needs and pass it.
- Process-wide setup that the product also does once (`runner.AdoptOrphans`)
  goes in `TestMain`, so no test depends on which test ran first.
- Signal handlers are process-wide: in-process `stew run` and `page` install
  them. A test that signals the test binary itself must not run in parallel
  with in-process `stew`; the signal would reach that `stew` too.
- A test that runs inside a git hook inherits `GIT_DIR`, `GIT_INDEX_FILE`, and
  similar variables. Anything that runs `git` must get an explicit environment,
  or the test acts on the repository being committed to.

## Parallelism

- Mark independent tests and subtests `t.Parallel()`.
- Assertions must not depend on load. Prefer an absolute bound derived from the
  spec over a comparison with a baseline run: a baseline measured under
  different load drifts.
- If a test only passes when it runs alone, fix the assertion, not the
  scheduling.

## Signals and Processes

- Two instances of a standard signal that are pending together merge into one.
  A count of deliveries can be lower than the number sent, never higher. So a
  count cannot prove "delivered once": a duplicate sent back to back merges and
  goes unseen. Back a count with a structural check (for example, the command's
  process group differs from stew's) or a unit test of the forwarding code.
- When several different signals are pending, Linux delivers the lowest number
  first, and `sh` runs traps in that order.
- A forwarded signal a wrapper does not trap ends the wrapper, and the exit
  code changes with it. Assert the code the whole chain produces.
- `kill -0` succeeds on a zombie. When a test binary is a subreaper, orphans
  become its zombies and are never reaped. To test whether a process has
  exited, check `/proc/<pid>/stat` for state `Z` or `X`, or check right after
  a call that reaps.

## Verifying a Test

Run every step before calling a test done.

1. Mutation: break the behavior the test covers (drop the call, skip the
   cleanup, forward a signal again after a delay). The test must fail. Revert
   the break. A mutation the test cannot see by design, such as a back-to-back
   duplicate signal, needs a different kind of check (see Signals and
   Processes).
2. Stress: `go test -count=20` on the package, and `go test -race -count=5`.
   Run the whole package, not just the test: load changes timing.
3. Timing: `go test -json` and compare `Elapsed` with and without `-race`.
   About 1 s extra under `-race` per test means one successful subprocess;
   more means waits or several subprocesses. Look first at the slowest script
   under `TestScripts`, which gates the package, and then at the slowest
   package, which gates the suite.
4. Failure output: read it once. It must say what broke without rerunning.

## CI

`stew.toml` defines the levels:

| Level | Runs | Used by |
| --- | --- | --- |
| `ci.quick` | `go test ./...` | pre-commit hook |
| `ci.full` | `go test -race ./...` | pre-push hook |

- A commit on main must pass both levels, not only the level its hook runs.
- A test that fails once in CI is flaky until proven otherwise. Reproduce it
  with `-count` under full-package load before changing anything.
- If `-race` time jumps, look for new successful subprocesses first.
- The pre-commit hook runs the suite inside a git hook. See Process-Global
  State for what that environment leaks.

## Review Checklist

- [ ] No fixed sleep or timed negative wait.
- [ ] Every loop is bounded, and a timed-out poll is asserted.
- [ ] Each negative check rests on a guarantee of the code under test.
- [ ] A count of signals meant to prove "once" is backed by a structural or unit
      check.
- [ ] No `exec stew` or `sh -c` wrapper without a reason from Processes.
- [ ] No process-global reads in code under test; no env or cwd mutation in
      tests.
- [ ] Independent tests are parallel; assertions hold under load.
- [ ] Exact exit codes and output are asserted, not only failure.
- [ ] Mutation, stress, and timing checks were run.
