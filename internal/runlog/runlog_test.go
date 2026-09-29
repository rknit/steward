package runlog

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 25, 4, 36, 1, 0, time.FixedZone("ICT", 7*3600))

func TestCreate(t *testing.T) {
	t.Parallel()
	stew := t.TempDir()
	run, err := Create(stew, now, bytes.NewReader([]byte{0x3f, 0x9a}))
	if err != nil {
		t.Fatal(err)
	}
	if run.ID != "20260924T213601Z-3f9a" {
		t.Errorf("ID = %q", run.ID)
	}
	if run.Dir != filepath.Join(stew, "runs", run.ID) {
		t.Errorf("Dir = %q", run.Dir)
	}
	if info, err := os.Stat(run.Dir); err != nil || !info.IsDir() {
		t.Errorf("run dir: %v", err)
	}
}

func TestCreateLocksRun(t *testing.T) {
	t.Parallel()
	run, err := Create(t.TempDir(), now, bytes.NewReader([]byte{0x3f, 0x9a}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockDir(run.Dir, nil); !errors.Is(err, ErrRunning) {
		t.Fatalf("lockDir while the run is open = %v, want ErrRunning", err)
	}
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
	lock, err := lockDir(run.Dir, nil)
	if err != nil {
		t.Fatalf("lockDir after Close = %v", err)
	}
	lock.Close()
}

func TestCreateRetriesWhenPruneTakesTheDirectory(t *testing.T) {
	t.Parallel()
	interfere := map[string]func(t *testing.T, dir string){
		"removed before the lock": func(t *testing.T, dir string) {
			if err := os.Remove(dir); err != nil {
				t.Fatal(err)
			}
		},
		"replaced before the lock": func(t *testing.T, dir string) {
			if err := os.Remove(dir); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"locked by prune": func(t *testing.T, dir string) {
			f, err := os.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { f.Close() })
			if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, act := range interfere {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			first := true
			beforeFlock := func(dir string) {
				if first {
					first = false
					act(t, dir)
				}
			}
			run, err := create(t.TempDir(), now, bytes.NewReader([]byte{0x00, 0x01, 0x00, 0x02}), beforeFlock)
			if err != nil {
				t.Fatal(err)
			}
			defer run.Close()
			if run.ID != "20260924T213601Z-0002" {
				t.Errorf("ID = %q, want a retry with the second suffix", run.ID)
			}
			if _, err := lockDir(run.Dir, nil); !errors.Is(err, ErrRunning) {
				t.Errorf("lockDir on the created run = %v, want ErrRunning", err)
			}
		})
	}
}

func TestCreateRetriesOnCollision(t *testing.T) {
	t.Parallel()
	stew := t.TempDir()
	rand := bytes.NewReader([]byte{0x00, 0x01, 0x00, 0x01, 0x00, 0x02})
	first, err := Create(stew, now, rand)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Create(stew, now, rand)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "20260924T213601Z-0001" || second.ID != "20260924T213601Z-0002" {
		t.Errorf("IDs = %q, %q", first.ID, second.ID)
	}
}

func TestIDsSortByTime(t *testing.T) {
	t.Parallel()
	re := regexp.MustCompile(`^\d{8}T\d{6}Z-[0-9a-f]{4}$`)
	a, _ := Create(t.TempDir(), now, bytes.NewReader([]byte{0xff, 0xff}))
	b, _ := Create(t.TempDir(), now.Add(time.Second), bytes.NewReader([]byte{0x00, 0x00}))
	if !re.MatchString(a.ID) || !re.MatchString(b.ID) || !(a.ID < b.ID) {
		t.Errorf("IDs %q, %q", a.ID, b.ID)
	}
}

func TestCreateErrors(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(file, now, bytes.NewReader([]byte{1, 2})); err == nil {
		t.Error("Create under a file succeeded")
	}
	if _, err := Create(t.TempDir(), now, bytes.NewReader(nil)); err == nil {
		t.Error("Create with no randomness succeeded")
	}
}

func TestSectionLog(t *testing.T) {
	t.Parallel()
	run, err := Create(t.TempDir(), now, bytes.NewReader([]byte{1, 2}))
	if err != nil {
		t.Fatal(err)
	}
	log, err := run.OpenSection("api", "ci.quick")
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Marker("verify", "test -f x"); err != nil {
		t.Fatal(err)
	}
	io.WriteString(log.Stdout(), "out1\n")
	io.WriteString(log.Stderr(), "err1\n")
	if err := log.Marker("run", "make"); err != nil {
		t.Fatal(err)
	}
	io.WriteString(log.Stdout(), "out2\n")
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(run.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if got, want := read("api:ci.quick.stdout"), "--- stew: verify: test -f x\nout1\n--- stew: run: make\nout2\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got, want := read("api:ci.quick.stderr"), "--- stew: verify: test -f x\nerr1\n--- stew: run: make\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
	if got, want := read("api:ci.quick.log"), "--- stew: verify: test -f x\nout1\nerr1\n--- stew: run: make\nout2\n"; got != want {
		t.Errorf("log = %q, want %q", got, want)
	}

	if _, err := run.OpenSection("api", "ci.quick"); err == nil {
		t.Error("reopening an existing section log succeeded")
	}
}

func TestOpenSectionExistingLog(t *testing.T) {
	t.Parallel()
	run, err := Create(t.TempDir(), now, bytes.NewReader([]byte{1, 2}))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run.Dir, "api:build.log"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run.OpenSection("api", "build"); err == nil {
		t.Error("OpenSection with an existing .log succeeded")
	}
}

func TestOpenSectionError(t *testing.T) {
	t.Parallel()
	run := &Run{ID: "x", Dir: filepath.Join(t.TempDir(), "gone")}
	if _, err := run.OpenSection("api", "build"); err == nil {
		t.Error("OpenSection in a missing dir succeeded")
	}
}
