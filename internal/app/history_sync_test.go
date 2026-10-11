package app

import (
	"testing"
	"time"
)

func TestDisconnectWindowIsConsumedOnce(t *testing.T) {
	window := &disconnectWindow{}
	lastMessageAt := time.Now().Add(-2 * time.Hour)
	window.MarkDisconnected("first-mid", &lastMessageAt)
	// Repeated login_required probes must not move the recovery boundary.
	time.Sleep(time.Millisecond)
	window.MarkDisconnected("later-mid", nil)

	from, to, stopMID := window.TakeRecoveryWindow()
	if from == nil || to == nil {
		t.Fatal("expected a recovery window")
	}
	if !from.Equal(lastMessageAt) || to.Before(*from) {
		t.Fatalf("recovery window has wrong bounds: from=%v to=%v, want from=%v", from, to, lastMessageAt)
	}
	if stopMID != "first-mid" {
		t.Fatalf("stopMID = %q, want first-mid", stopMID)
	}

	from, to, stopMID = window.TakeRecoveryWindow()
	if from != nil || to != nil || stopMID != "" {
		t.Fatalf("expected consumed recovery window, got from=%v to=%v stopMID=%q", from, to, stopMID)
	}
}

func TestHistorySyncBoundaryReached(t *testing.T) {
	tests := []struct {
		name              string
		stopMID           string
		messageMID        string
		existedAtSnapshot bool
		wantReached       bool
		wantReason        string
	}{
		{
			name:        "exact startup boundary",
			stopMID:     "100",
			messageMID:  "100",
			wantReached: true,
			wantReason:  "stop_mid_reached",
		},
		{
			name:              "existing message does not hide older gaps",
			stopMID:           "100",
			messageMID:        "99",
			existedAtSnapshot: true,
			wantReached:       false,
		},
		{
			name:              "message inserted after snapshot does not stop recovery",
			stopMID:           "100",
			messageMID:        "101",
			existedAtSnapshot: false,
			wantReached:       false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reached, reason := historySyncBoundaryReached("recovery", test.stopMID, test.messageMID, test.existedAtSnapshot)
			if reached != test.wantReached || reason != test.wantReason {
				t.Fatalf("historySyncBoundaryReached() = (%v, %q), want (%v, %q)", reached, reason, test.wantReached, test.wantReason)
			}
		})
	}
}

func TestStartupHistorySyncStillStopsAtExistingMessage(t *testing.T) {
	reached, reason := historySyncBoundaryReached("startup", "unavailable-mid", "99", true)
	if !reached || reason != "existing_message_reached" {
		t.Fatalf("startup history boundary = (%v, %q), want (true, existing_message_reached)", reached, reason)
	}
}

func TestManualHistorySyncDoesNotStopAtExistingMessage(t *testing.T) {
	reached, reason := historySyncBoundaryReached("manual", "100", "100", true)
	if reached || reason != "" {
		t.Fatalf("manual history boundary = (%v, %q), want (false, empty)", reached, reason)
	}
}
