package store

import (
	"testing"
	"time"
)

func TestMediaResolutionRetryDelay(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 0, want: time.Second},
		{attempt: 1, want: time.Second},
		{attempt: 2, want: 2 * time.Second},
		{attempt: 5, want: 16 * time.Second},
		{attempt: 20, want: 5 * time.Minute},
	}
	for _, test := range tests {
		if got := mediaResolutionRetryDelay(test.attempt); got != test.want {
			t.Fatalf("attempt %d: delay = %s, want %s", test.attempt, got, test.want)
		}
	}
}
