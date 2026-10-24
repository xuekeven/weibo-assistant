package browser

import (
	"testing"
	"time"
)

func TestChatWebSocketStateReloadAndLateReconnect(t *testing.T) {
	type change struct{ connected, reconnected bool }
	var changes []change
	state := &chatWebSocketState{onState: func(connected, reconnected bool) {
		changes = append(changes, change{connected, reconnected})
	}}
	state.Created("old")
	if state.Connected() {
		t.Fatal("created socket has not completed handshake")
	}
	state.Handshake("old")
	state.Reset() // page reload; Chrome need not report a close for old
	state.Created("new")
	state.Handshake("new")
	if !state.Closed("new") || state.Connected() {
		t.Fatal("current socket close must disconnect")
	}
	state.Created("later") // hours later; stale old close must not affect it
	state.Handshake("later")
	if state.Closed("old") || !state.Connected() {
		t.Fatal("stale close changed current socket")
	}
	want := []change{{true, false}, {false, false}, {true, true}, {false, false}, {true, true}}
	if len(changes) != len(want) {
		t.Fatalf("changes = %v, want %v", changes, want)
	}
	for i := range want {
		if changes[i] != want[i] {
			t.Fatalf("change %d = %v, want %v", i, changes[i], want[i])
		}
	}
}

func TestChatWebSocketStateFindsSocketAlreadyOpenAtAttach(t *testing.T) {
	connected := 0
	state := &chatWebSocketState{onState: func(ok, reconnect bool) {
		if ok && !reconnect {
			connected++
		}
	}}
	state.FrameReceived("existing")
	state.FrameReceived("existing")
	if !state.Connected() || connected != 1 {
		t.Fatalf("existing socket connected=%v notifications=%d", state.Connected(), connected)
	}
}

func TestChatWebSocketStateDetectsStalledReconnect(t *testing.T) {
	state := &chatWebSocketState{}
	state.Reset()
	state.Created("first")
	if state.DisconnectedFor(time.Now().Add(time.Minute), 2*time.Minute) {
		t.Fatal("reconnect timeout fired too soon")
	}
	state.Created("second") // another failed attempt must not postpone recovery forever
	if !state.DisconnectedFor(time.Now().Add(3*time.Minute), 2*time.Minute) {
		t.Fatal("reconnect timeout was postponed by an unconfirmed socket")
	}
	state.Handshake("second")
	if state.DisconnectedFor(time.Now().Add(3*time.Minute), 2*time.Minute) {
		t.Fatal("connected socket must clear the timeout")
	}
	state.Closed("second")
	if !state.DisconnectedFor(time.Now().Add(3*time.Minute), 2*time.Minute) {
		t.Fatal("closed socket must start a new recovery timeout")
	}
}
