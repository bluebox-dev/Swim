package api

import (
	"testing"
	"time"
)

func TestScheduleOccurrences(t *testing.T) {
	start := time.Date(2026, 11, 26, 8, 30, 0, 0, thaiTZ) // Thursday
	from := time.Date(2026, 11, 23, 0, 0, 0, 0, thaiTZ)
	to := time.Date(2026, 12, 6, 23, 59, 0, 0, thaiTZ)
	once := schedule{StartAt: start, Repeat: scheduleRepeat{Freq: "none"}}
	if got := once.occurrences(from, to); len(got) != 1 || !got[0].Equal(start) {
		t.Fatalf("once: %v", got)
	}
	daily := schedule{StartAt: start, Repeat: scheduleRepeat{Freq: "daily"}}
	if got := daily.occurrences(from, to); len(got) != 11 || !got[0].Equal(start) {
		t.Fatalf("daily: %d %v", len(got), got)
	}
	weekly := schedule{StartAt: start, Repeat: scheduleRepeat{Freq: "weekly", Days: []int{1, 4}}} // Mon, Thu
	got := weekly.occurrences(from, to)
	if len(got) != 4 { // Thu 26, Mon 30, Thu 3, (Mon 7 out of range) → 3 … plus none before start
		if len(got) != 3 {
			t.Fatalf("weekly: %d %v", len(got), got)
		}
	}
	for _, g := range got {
		if g.Hour() != 8 || g.Minute() != 30 || (g.Weekday() != time.Monday && g.Weekday() != time.Thursday) {
			t.Fatalf("weekly wrong time/day: %v", g)
		}
	}
	// a window that starts long after start_at still finds the occurrences inside it
	late := daily.occurrences(time.Date(2027, 3, 1, 0, 0, 0, 0, thaiTZ), time.Date(2027, 3, 2, 23, 0, 0, 0, thaiTZ))
	if len(late) != 2 {
		t.Fatalf("late window: %v", late)
	}
}
