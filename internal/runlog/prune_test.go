package runlog

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestRetentionExpired(t *testing.T) {
	ids := []string{
		"20260101T000000Z-0000",
		"20260102T000000Z-0001",
		"20260103T000000Z-0002",
		"20260104T000000Z-0003",
		"20261399T000000Z-0004",
	}
	jan := func(day int) *time.Time { return new(time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC)) }
	tests := []struct {
		name string
		keep Retention
		want []string
	}{
		{"last 2", Retention{LastN: new(2)}, ids[:3]},
		{"last 0", Retention{LastN: new(0)}, ids},
		{"last more than all", Retention{LastN: new(10)}, nil},
		{"since keeps the boundary, not an invalid date", Retention{Since: jan(3)}, []string{ids[0], ids[1], ids[4]}},
		{"since in another zone", Retention{Since: new(time.Date(2026, 1, 3, 6, 0, 0, 0, time.FixedZone("ICT", 7*3600)))}, []string{ids[0], ids[1], ids[4]}},
		{"both keep the union", Retention{Since: jan(2), LastN: new(1)}, ids[:1]},
		{"no rule keeps nothing", Retention{}, ids},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.keep.Expired(ids); !slices.Equal(got, tt.want) {
				t.Errorf("Expired = %q, want %q", got, tt.want)
			}
		})
	}
	if got := (Retention{LastN: new(0)}).Expired(nil); got != nil {
		t.Errorf("Expired(nil) = %q", got)
	}
}

func TestDelete(t *testing.T) {
	stew := t.TempDir()
	run, err := Create(stew, now, bytes.NewReader([]byte{0x3f, 0x9a}))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run.Dir, "core-setup.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Delete(stew, run.ID); !errors.Is(err, ErrRunning) {
		t.Fatalf("Delete of a locked run = %v, want ErrRunning", err)
	}
	if _, err := os.Stat(run.Dir); err != nil {
		t.Fatalf("locked run was touched: %v", err)
	}
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Delete(stew, run.ID); err != nil {
		t.Fatalf("Delete = %v", err)
	}
	if _, err := os.Stat(run.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("run dir still there: %v", err)
	}
	if err := Delete(stew, run.ID); !errors.Is(err, ErrUnknownRun) {
		t.Errorf("second Delete = %v, want ErrUnknownRun", err)
	}
}

func TestDeleteErrors(t *testing.T) {
	stew := t.TempDir()
	runs := filepath.Join(stew, "runs")
	target := filepath.Join(stew, "target")
	for _, dir := range []string{runs, target} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(runs, "20260101T000000Z-0000")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(runs, "20260101T000000Z-0001")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../target", "latest", "20260101T000000Z-0000", "20260101T000000Z-0001", "20260101T000000Z-0002"} {
		if err := Delete(stew, id); !errors.Is(err, ErrUnknownRun) {
			t.Errorf("Delete(%q) = %v, want ErrUnknownRun", id, err)
		}
	}
	for _, p := range []string{target, link, file} {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s was touched: %v", p, err)
		}
	}

	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := filepath.Join(runs, "20260101T000000Z-0003")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	err := Delete(stew, "20260101T000000Z-0003")
	if err == nil || errors.Is(err, ErrUnknownRun) || errors.Is(err, ErrRunning) {
		t.Errorf("Delete of an unremovable run = %v, want a removal error", err)
	}
}
