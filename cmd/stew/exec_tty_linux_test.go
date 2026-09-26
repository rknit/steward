package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// shellSession is an interactive bash on a pseudo-terminal, in a workspace with project "lib", whose project wrapper
// forks. stew is this test binary.
type shellSession struct {
	t      *testing.T
	dir    string
	master *os.File
	bash   *exec.Cmd

	mu  sync.Mutex
	out bytes.Buffer
	pos int
}

func newShellSession(t *testing.T) *shellSession {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		".stew/config.toml":   "workspace_wrapper = \"\"\n",
		".stew/projects.toml": "projects = [\"lib\"]\n",
		"lib/stew.toml":       "name = \"lib\"\nproject_wrapper = \"wrap-fork {{STEW_STEP}}\"\n[setup]\nrun = \"true\"\n",
		"bin/wrap-fork":       "#!/bin/sh\n\"$@\"\nexit $?\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"stew", "hupcount"} {
		if err := os.Symlink(self, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	tmp := filepath.Join(dir, "tmp")
	if err := os.Mkdir(tmp, 0o755); err != nil {
		t.Fatal(err)
	}

	master, slave := openPTY(t)
	defer slave.Close()
	bash := exec.Command("bash", "--norc", "--noprofile", "--noediting", "-i")
	bash.Dir = dir
	bash.Env = []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + dir, "TMPDIR=" + tmp, "TERM=dumb", "PS1=$ ", "HISTFILE=/dev/null",
	}
	bash.Stdin, bash.Stdout, bash.Stderr = slave, slave, slave
	bash.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := bash.Start(); err != nil {
		t.Fatal(err)
	}
	s := &shellSession{t: t, dir: dir, master: master, bash: bash}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			s.mu.Lock()
			s.out.Write(buf[:n])
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		bash.Process.Kill()
		bash.Wait()
		master.Close()
	})
	return s
}

func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	// Non-blocking, so the runtime poller owns it and Close ends a pending Read. Otherwise the Read keeps the
	// master open, and closing it never hangs up the terminal.
	fd, err := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	master = os.NewFile(uintptr(fd), "/dev/ptmx")
	var unlock int32
	if err := ioctl(master, syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)); err != nil {
		t.Fatal(err)
	}
	var n uint32
	if err := ioctl(master, syscall.TIOCGPTN, unsafe.Pointer(&n)); err != nil {
		t.Fatal(err)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	return master, slave
}

func ioctl(f *os.File, req uint, arg unsafe.Pointer) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(req), uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}

// send types text into the terminal.
func (s *shellSession) send(text string) {
	s.t.Helper()
	if _, err := s.master.Write([]byte(text)); err != nil {
		s.t.Fatal(err)
	}
}

// expect waits for pattern in the output after the previous match and returns its submatches.
// Commands print computed markers, such as $((6*7)), so the echo of the typed line never matches.
func (s *shellSession) expect(pattern string) []string {
	s.t.Helper()
	re := regexp.MustCompile(pattern)
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.mu.Lock()
		rest := s.out.String()[s.pos:]
		loc := re.FindStringSubmatchIndex(rest)
		if loc != nil {
			m := make([]string, len(loc)/2)
			for i := range m {
				if loc[2*i] >= 0 {
					m[i] = rest[loc[2*i]:loc[2*i+1]]
				}
			}
			s.pos += loc[1]
			s.mu.Unlock()
			return m
		}
		all := s.out.String()
		s.mu.Unlock()
		if time.Now().After(deadline) {
			s.t.Fatalf("no match for %q in terminal output:\n%s", pattern, all)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *shellSession) lines(name string) int {
	s.t.Helper()
	data, err := os.ReadFile(filepath.Join(s.dir, name))
	if err != nil {
		s.t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

func TestExecTerminalInterrupt(t *testing.T) {
	t.Parallel()
	for _, project := range []string{"", "lib"} {
		t.Run(map[string]string{"": "plain", "lib": "wrapped"}[project], func(t *testing.T) {
			t.Parallel()
			s := newShellSession(t)
			s.send(`stew exec ` + project + ` 'trap "echo int >> ints" INT; echo "R-$((6*7))"; sleep 2; exit 5'; echo "rc=$((0+$?))"` + "\n")
			s.expect(`R-42`)
			s.send("\x03")
			if rc := s.expect(`rc=(\d+)`)[1]; rc != "5" {
				t.Errorf("exit = %s, want 5 (the command handles Ctrl-C and exits 5)", rc)
			}
			if n := s.lines("ints"); n != 1 {
				t.Errorf("command got SIGINT %d times, want 1", n)
			}

			s.send(`stew exec ` + project + ` 'echo "R-$((6*7))"; sleep 5'; echo "rc=$((0+$?))"` + "\n")
			s.expect(`R-42`)
			s.send("\x03")
			m := s.expect(`(?s)(.*)rc=(\d+)`)
			if m[2] != "130" {
				t.Errorf("exit = %s, want 130", m[2])
			}
			if strings.Contains(m[1], "stew:") {
				t.Errorf("stew reported an error after Ctrl-C:\n%s", m[1])
			}
		})
	}
}

func TestExecTerminalSuspend(t *testing.T) {
	t.Parallel()
	for _, project := range []string{"", "lib"} {
		t.Run(map[string]string{"": "plain", "lib": "wrapped"}[project], func(t *testing.T) {
			t.Parallel()
			s := newShellSession(t)
			s.send(`stew exec ` + project + ` 'echo "R-$((6*7))"; read line; echo "got:$line"'; echo "rc=$((0+$?))"` + "\n")
			s.expect(`R-42`)
			s.send("\x1a")
			s.expect(`Stopped`)
			if rc := s.expect(`rc=(\d+)`)[1]; rc != "148" {
				t.Errorf("exit at the stop = %s, want 148 (128+SIGTSTP)", rc)
			}
			s.send("fg\n")
			s.expect(`stew exec`)
			s.send("hello\n")
			s.expect(`got:hello`)
			s.send(`echo "rc=$((0+$?))"` + "\n")
			if rc := s.expect(`rc=(\d+)`)[1]; rc != "0" {
				t.Errorf("exit = %s, want 0", rc)
			}
		})
	}
}

func TestExecTerminalTermSparesPipeline(t *testing.T) {
	t.Parallel()
	s := newShellSession(t)
	s.send(`stew exec 'echo $PPID > stewpid; echo $$ > cmdpid; echo "R-$((6*7))"; sleep 5' | { cat; echo "sibling alive" > sibling; } &` + "\n")
	s.expect(`R-42`)
	s.send(`kill -TERM $(cat stewpid); wait; echo "W-$((6*7))"; cat sibling; kill -0 $(cat cmdpid) 2>/dev/null && echo "command alive"` + "\n")
	m := s.expect(`(?s)W-42(.*)\$ `)
	if !strings.Contains(m[1], "sibling alive") {
		t.Errorf("SIGTERM to stew reached the rest of the pipeline:\n%s", m[1])
	}
	if strings.Contains(m[1], "command alive") {
		t.Errorf("SIGTERM to stew did not end the command:\n%s", m[1])
	}
}

// A hangup reaches the command, and no more often under stew than when bash runs it directly. Two SIGHUPs that
// arrive together merge into one, so the count under stew may be lower.
func TestExecTerminalHangup(t *testing.T) {
	commands := map[string]string{
		"direct":  `hupcount hups`,
		"plain":   `stew exec 'hupcount hups'`,
		"wrapped": `stew exec lib 'hupcount hups'`,
	}
	var mu sync.Mutex
	hups := map[string]int{}
	t.Run("count", func(t *testing.T) {
		for name, command := range commands {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				s := newShellSession(t)
				s.send(command + "\n")
				s.expect(`hupcount ready`)
				s.master.Close()
				time.Sleep(1500 * time.Millisecond)
				n := s.lines("hups")
				mu.Lock()
				hups[name] = n
				mu.Unlock()
			})
		}
	})
	if t.Failed() {
		return
	}
	if len(hups) != len(commands) {
		t.Skip("a terminal session was skipped")
	}
	direct := hups["direct"]
	for _, name := range []string{"plain", "wrapped"} {
		if got := hups[name]; got < 1 || got > direct {
			t.Errorf("%s: command got SIGHUP %d times under stew, %d times directly; want 1 to %d", name, got, direct, direct)
		}
	}
	t.Logf("SIGHUPs on hangup: %d", direct)
}
