package tasks

import (
	"errors"
	"fmt"
	"time"
	_ "time/tzdata"
)

type Schedule struct {
	Enabled bool   `json:"enabled"`
	Kind    string `json:"kind"`
	At      string `json:"at"`
	Weekday int    `json:"weekday"`
	Minutes int    `json:"minutes"`
	Zone    string `json:"zone"`
}

func NextRun(s Schedule, after time.Time) (time.Time, error) {
	var zero time.Time
	if !s.Enabled {
		return zero, nil
	}
	loc, err := time.LoadLocation(s.Zone)
	if err != nil {
		return zero, errors.New("invalid schedule timezone")
	}
	if s.Kind == "interval" {
		if s.Minutes < 1 || s.Minutes > 10080 {
			return zero, errors.New("interval must be 1–10080 minutes")
		}
		return after.Add(time.Duration(s.Minutes) * time.Minute), nil
	}
	if s.Kind != "daily" && s.Kind != "weekly" {
		return zero, errors.New("invalid schedule frequency")
	}
	clock, err := time.Parse("15:04", s.At)
	if err != nil {
		return zero, errors.New("time must be HH:MM")
	}
	if s.Kind == "weekly" && (s.Weekday < 0 || s.Weekday > 6) {
		return zero, errors.New("invalid weekday")
	}
	a := after.In(loc)
	for day := 0; day < 15; day++ {
		candidate := time.Date(a.Year(), a.Month(), a.Day()+day, clock.Hour(), clock.Minute(), 0, 0, loc)
		// A DST gap must not silently run at a different wall-clock time.
		if candidate.Hour() != clock.Hour() || candidate.Minute() != clock.Minute() {
			continue
		}
		if s.Kind == "weekly" && int(candidate.Weekday()) != s.Weekday {
			continue
		}
		if candidate.After(after) {
			return candidate, nil
		}
	}
	return zero, fmt.Errorf("no valid execution time in schedule horizon")
}
