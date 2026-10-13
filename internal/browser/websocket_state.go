package browser

import "sync"

// chatWebSocketState follows the latest chat socket. Reloaded pages may never
// emit close events for their old sockets, so counting all created sockets is
// not a reliable indication that messages can still arrive.
type chatWebSocketState struct {
	mu        sync.Mutex
	currentID string
	hadSocket bool
	connected bool
	onState   func(bool, bool)
}

func (s *chatWebSocketState) Created(id string) {
	s.mu.Lock()
	wasConnected := s.connected
	s.currentID = id
	s.connected = false
	s.mu.Unlock()
	if wasConnected && s.onState != nil {
		s.onState(false, false)
	}
}

func (s *chatWebSocketState) Handshake(id string) {
	s.mu.Lock()
	current := id != "" && id == s.currentID
	reconnected := s.hadSocket && !s.connected
	shouldNotify := current && !s.connected
	if shouldNotify {
		s.connected = true
		s.hadSocket = true
	}
	s.mu.Unlock()
	if shouldNotify && s.onState != nil {
		s.onState(true, reconnected)
	}
}

// FrameReceived also confirms a socket that was already open when CDP began
// observing the page, so its creation and handshake events were missed.
func (s *chatWebSocketState) FrameReceived(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	if s.currentID == "" {
		s.currentID = id
	}
	s.mu.Unlock()
	s.Handshake(id)
}

func (s *chatWebSocketState) Closed(id string) bool {
	s.mu.Lock()
	current := id != "" && id == s.currentID
	wasConnected := s.connected
	if current {
		s.currentID = ""
		s.connected = false
	}
	s.mu.Unlock()
	if current && wasConnected && s.onState != nil {
		s.onState(false, false)
	}
	return current
}

func (s *chatWebSocketState) Reset() {
	s.mu.Lock()
	wasConnected := s.connected
	s.currentID = ""
	s.connected = false
	s.mu.Unlock()
	if wasConnected && s.onState != nil {
		s.onState(false, false)
	}
}

func (s *chatWebSocketState) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

func (s *chatWebSocketState) EverConnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hadSocket
}
