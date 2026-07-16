package app

import "testing"

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
			name:              "existing message protects against unreachable boundary",
			stopMID:           "unavailable-event-mid",
			messageMID:        "99",
			existedAtSnapshot: true,
			wantReached:       true,
			wantReason:        "existing_message_reached",
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
			reached, reason := historySyncBoundaryReached(test.stopMID, test.messageMID, test.existedAtSnapshot)
			if reached != test.wantReached || reason != test.wantReason {
				t.Fatalf("historySyncBoundaryReached() = (%v, %q), want (%v, %q)", reached, reason, test.wantReached, test.wantReason)
			}
		})
	}
}
