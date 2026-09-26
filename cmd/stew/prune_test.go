package main

import (
	"testing"
	"time"
)

func TestParseKeepSince(t *testing.T) {
	t.Parallel()
	ict := time.FixedZone("ICT", 7*3600)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

	valid := []struct {
		in   string
		want time.Time
	}{
		{"36h", now.Add(-36 * time.Hour)},
		{"7d", now.Add(-7 * 24 * time.Hour)},
		{"2w", now.Add(-14 * 24 * time.Hour)},
		{"1d12h", now.Add(-36 * time.Hour)},
		{"90m", now.Add(-90 * time.Minute)},
		{"30s", now.Add(-30 * time.Second)},
		{"0d", now},
		{"2026-09-01", time.Date(2026, 9, 1, 0, 0, 0, 0, ict)},
		{"2026-09-01 15:04:05", time.Date(2026, 9, 1, 15, 4, 5, 0, ict)},
		{"2026-09-01T15:04:05Z", time.Date(2026, 9, 1, 15, 4, 5, 0, time.UTC)},
		{"2026-09-01T15:04:05+07:00", time.Date(2026, 9, 1, 15, 4, 5, 0, ict)},
	}
	for _, tt := range valid {
		got, err := parseKeepSince(tt.in, now, ict)
		if err != nil || !got.Equal(tt.want) {
			t.Errorf("parseKeepSince(%q) = %v, %v; want %v", tt.in, got, err, tt.want)
		}
	}

	for _, in := range []string{
		"", "7", "d", "-7d", "7D", "1.5d", "7 d", "7y", "2026-13-01", "2026-09-01T15:04:05",
		"99999999999999999999d", "9999999999999w",
	} {
		if got, err := parseKeepSince(in, now, ict); err == nil {
			t.Errorf("parseKeepSince(%q) = %v, want an error", in, got)
		}
	}
}
