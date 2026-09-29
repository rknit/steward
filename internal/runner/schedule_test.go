package runner

import (
	"context"
	"maps"
	"slices"
	"syscall"
	"testing"
)

func cells(res *Results) map[string]Status {
	got := map[string]Status{}
	for _, row := range res.Rows {
		for i, c := range row.Cells {
			if c != "" {
				got[row.Project+":"+res.Columns[i]] = c
			}
		}
	}
	return got
}

func TestJobLimitCapsRunningSections(t *testing.T) {
	t.Parallel()
	g := startGated(context.Background(), 2, plan(sec("a", "build", "a"), sec("b", "build", "b"), sec("c", "build", "c")))
	g.expect(t, "start a:build", "start b:build")
	g.exec.finish("b", Result{})
	g.expect(t, "end b:build done", "start c:build")
	g.exec.finish("a", Result{})
	g.expect(t, "end a:build done")
	g.exec.finish("c", Result{})
	g.expect(t, "end c:build done")
	res := g.wait(t)
	if res.ExitCode() != 0 {
		t.Errorf("exit = %d", res.ExitCode())
	}
}

func TestFailureBlocksOnlyDependentsWhileSiblingsRun(t *testing.T) {
	t.Parallel()
	g := startGated(context.Background(), 2,
		plan(sec("a", "build", "a"), sec("b", "build", "b"), sec("a", "test", "at", "a:build")))
	g.expect(t, "start a:build", "start b:build")
	g.exec.finish("a", Result{ExitCode: 1})
	g.expect(t, "end a:build fail", "blocked a:test by a:build")
	g.exec.finish("b", Result{})
	g.expect(t, "end b:build done")
	res := g.wait(t)
	want := map[string]Status{"a:build": Fail, "b:build": Done, "a:test": Blocked}
	if got := cells(res); !maps.Equal(got, want) {
		t.Errorf("cells = %v, want %v", got, want)
	}
	if res.ExitCode() != 1 {
		t.Errorf("exit = %d", res.ExitCode())
	}
}

func TestInterruptStopsEveryRunningSection(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	g := startGated(ctx, 3, plan(sec("a", "build", "a"), sec("b", "build", "b"), sec("c", "build", "c"),
		sec("a", "test", "at", "a:build")))
	g.expect(t, "start a:build", "start b:build", "start c:build")
	g.exec.finish("c", Result{})
	g.expect(t, "end c:build done")
	cancel(ErrInterrupted)
	g.expectAnyOrder(t, "end a:build interrupted", "end b:build interrupted")
	res := g.wait(t)
	want := map[string]Status{"a:build": Interrupted, "b:build": Interrupted, "c:build": Done}
	if got := cells(res); !maps.Equal(got, want) {
		t.Errorf("cells = %v, want %v", got, want)
	}
	if res.Interrupted != syscall.SIGINT || res.ExitCode() != 130 {
		t.Errorf("interrupted = %v, exit = %d", res.Interrupted, res.ExitCode())
	}
}

func TestEmptySectionsEachStartAndEnd(t *testing.T) {
	t.Parallel()
	h := newHarness(nil)
	h.r.Jobs = 2
	res := h.r.Run(context.Background(), plan(sec("a", "all", ""), sec("b", "all", ""), sec("c", "all", "", "a:all")))
	want := map[string]Status{"a:all": Skip, "b:all": Skip, "c:all": Skip}
	if got := cells(res); !maps.Equal(got, want) {
		t.Errorf("cells = %v, want %v", got, want)
	}
	if n := len(h.rec.events); n != 6 {
		t.Errorf("events = %q, want a start and an end for each section", h.rec.events)
	}
}

func TestExclusiveSectionRunsAlone(t *testing.T) {
	t.Parallel()
	x := sec("x", "build", "x")
	x.Exclusive = true
	g := startGated(context.Background(), 4, plan(sec("a", "build", "a"), x, sec("b", "build", "b")))
	g.expect(t, "start a:build")
	g.exec.finish("a", Result{})
	g.expect(t, "end a:build done", "start x:build")
	g.exec.finish("x", Result{})
	g.expect(t, "end x:build done", "start b:build")
	g.exec.finish("b", Result{})
	g.expect(t, "end b:build done")
	g.wait(t)
}

func TestBlockedExclusiveSectionDoesNotStopScan(t *testing.T) {
	t.Parallel()
	t.Run("passed over while not ready", func(t *testing.T) {
		t.Parallel()
		x := sec("x", "build", "x", "a:build")
		x.Exclusive = true
		g := startGated(context.Background(), 4, plan(sec("a", "build", "a"), x, sec("b", "build", "b")))
		g.expect(t, "start a:build", "start b:build")
		g.exec.finish("a", Result{ExitCode: 1})
		g.expect(t, "end a:build fail", "blocked x:build by a:build")
		g.exec.finish("b", Result{})
		g.expect(t, "end b:build done")
		res := g.wait(t)
		want := map[string]Status{"a:build": Fail, "x:build": Blocked, "b:build": Done}
		if got := cells(res); !maps.Equal(got, want) {
			t.Errorf("cells = %v, want %v", got, want)
		}
	})
	t.Run("blocked mid-scan", func(t *testing.T) {
		t.Parallel()
		x := sec("x", "build", "x", "a:build")
		x.Exclusive = true
		g := startGated(context.Background(), 2,
			plan(sec("a", "build", "a"), sec("b", "build", "b"), x, sec("d", "build", "d")))
		g.expect(t, "start a:build", "start b:build")
		g.exec.finish("a", Result{ExitCode: 1})
		g.expect(t, "end a:build fail", "blocked x:build by a:build", "start d:build")
		g.exec.finish("d", Result{})
		g.expect(t, "end d:build done")
		g.exec.finish("b", Result{})
		g.expect(t, "end b:build done")
		res := g.wait(t)
		want := map[string]Status{"a:build": Fail, "b:build": Done, "x:build": Blocked, "d:build": Done}
		if got := cells(res); !maps.Equal(got, want) {
			t.Errorf("cells = %v, want %v", got, want)
		}
	})
}

func TestSectionBeforeWaitingExclusiveStarts(t *testing.T) {
	t.Parallel()
	x := sec("x", "build", "x")
	x.Exclusive = true
	g := startGated(context.Background(), 4,
		plan(sec("a", "build", "a"), sec("p", "build", "p", "a:build"), x, sec("q", "build", "q")))
	g.expect(t, "start a:build")
	g.exec.finish("a", Result{})
	g.expect(t, "end a:build done", "start p:build")
	g.exec.finish("p", Result{})
	g.expect(t, "end p:build done", "start x:build")
	g.exec.finish("x", Result{})
	g.expect(t, "end x:build done", "start q:build")
	g.exec.finish("q", Result{})
	g.expect(t, "end q:build done")
	g.wait(t)
}

func TestSerialProjectRunsOneSectionAtATime(t *testing.T) {
	t.Parallel()
	one, two := sec("p", "one", "p1"), sec("p", "two", "p2")
	one.Serial, two.Serial = true, true
	g := startGated(context.Background(), 4, plan(one, two, sec("q", "one", "q1")))
	g.expect(t, "start p:one", "start q:one")
	g.exec.finish("p1", Result{})
	g.expect(t, "end p:one done", "start p:two")
	g.exec.finish("q1", Result{})
	g.expect(t, "end q:one done")
	g.exec.finish("p2", Result{})
	g.expect(t, "end p:two done")
	g.wait(t)
}

func TestStepKeysAreUniqueWhenNamesCollide(t *testing.T) {
	t.Parallel()
	h := newHarness(map[string][]fakeCmd{"x": {ok("")}, "y": {ok("")}})
	w := []string{"wrap {{STEW_STEP}}"}
	h.r.Run(context.Background(), plan(
		Section{Project: "a-b", Name: "c", Dir: "/w/a-b", Run: "x", Wrappers: w},
		Section{Project: "a", Name: "b-c", Dir: "/w/a", Run: "y", Wrappers: w},
	))
	if want := []string{"0-a-b-c", "1-a-b-c"}; !slices.Equal(h.steps.prepared, want) {
		t.Errorf("keys = %q, want %q", h.steps.prepared, want)
	}
}
