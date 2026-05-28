package timeparse

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		in      string
		want    time.Time
		wantErr bool
	}{
		{"date only", "2026-01-15", time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), false},
		{"datetime space", "2026-01-15 10:30:00", time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC), false},
		{"datetime T", "2026-01-15T10:30:00", time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC), false},
		{"rfc3339 zulu", "2026-01-15T10:30:00Z", time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC), false},
		{"relative seconds", "30s", now.Add(-30 * time.Second), false},
		{"relative minutes", "15m", now.Add(-15 * time.Minute), false},
		{"relative hours", "1h", now.Add(-1 * time.Hour), false},
		{"relative days", "2d", now.Add(-48 * time.Hour), false},
		{"relative weeks", "1w", now.Add(-7 * 24 * time.Hour), false},
		{"empty", "", time.Time{}, true},
		{"garbage", "not a date", time.Time{}, true},
		{"unknown unit", "5y", time.Time{}, true},
		{"negative", "-1h", time.Time{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.in, now)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if !tc.wantErr && !got.Equal(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
