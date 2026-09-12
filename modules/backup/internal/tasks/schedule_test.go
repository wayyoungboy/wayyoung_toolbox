package tasks

import (
	"testing"
	"time"
)

func TestSchedules(t *testing.T) {
	base := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		s    Schedule
		want string
	}{
		{"disabled", Schedule{Kind: "daily", At: "09:00", Zone: "Asia/Shanghai"}, ""},
		{"daily", Schedule{Enabled: true, Kind: "daily", At: "09:00", Zone: "Asia/Shanghai"}, "2026-09-03T01:00:00Z"},
		{"weekly", Schedule{Enabled: true, Kind: "weekly", At: "09:00", Weekday: 1, Zone: "Asia/Shanghai"}, "2026-09-07T01:00:00Z"},
		{"interval", Schedule{Enabled: true, Kind: "interval", Minutes: 30, Zone: "UTC"}, "2026-09-03T00:30:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NextRun(tc.s, base)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if !got.IsZero() {
					t.Fatal(got)
				}
				return
			}
			if got.UTC().Format(time.RFC3339) != tc.want {
				t.Fatal(got)
			}
		})
	}
	for _, s := range []Schedule{
		{Enabled: true, Kind: "interval", Minutes: 0, Zone: "UTC"},
		{Enabled: true, Kind: "daily", At: "25:90", Zone: "UTC"},
		{Enabled: true, Kind: "weekly", At: "02:00", Weekday: 7, Zone: "UTC"},
		{Enabled: true, Kind: "daily", At: "02:00", Zone: "invalid"},
		{Enabled: true, Kind: "unknown", Zone: "UTC"},
	} {
		if _, err := NextRun(s, base); err == nil {
			t.Fatalf("accepted %+v", s)
		}
	}
}

func TestDailyDSTGapSkipsNonexistentWallTime(t *testing.T) {
	s := Schedule{Enabled: true, Kind: "daily", At: "02:30", Zone: "America/New_York"}
	next, err := NextRun(s, time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if next.UTC().Format(time.RFC3339) != "2026-03-09T06:30:00Z" {
		t.Fatal(next)
	}
}
