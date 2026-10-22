package logs

import "sync"

// subBuffer is the per-subscriber channel capacity. A slow SSE client drops
// lines once its buffer fills; process output must never block on a client.
const subBuffer = 256

// subscriber is one live subscription to a site's log stream.
type subscriber struct {
	ch chan Line
}

// Subscribe returns a channel that receives new lines for siteID as they are
// written, and a function that ends the subscription. The channel is closed on
// unsubscribe, when the store closes, and immediately for an invalid site id or
// a closed store.
//
// Subscribers see only lines written after they subscribe; use Tail for the
// recent history. Delivery is best-effort: when a subscriber's buffer is full
// new lines are dropped for that subscriber so a stalled client cannot block a
// site's process.
func (s *Store) Subscribe(siteID string) (<-chan Line, func()) {
	if err := validateSiteID(siteID); err != nil {
		return closedLines(), func() {}
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return closedLines(), func() {}
	}
	st := s.siteLocked(siteID)
	id := s.nextSub
	s.nextSub++
	sub := &subscriber{ch: make(chan Line, subBuffer)}
	st.subs[id] = sub
	s.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if _, ok := st.subs[id]; !ok {
				return
			}
			delete(st.subs, id)
			close(sub.ch)
		})
	}
	return sub.ch, cancel
}

// fanOutLocked delivers line to every subscriber of st without blocking. The
// caller must hold s.mu, which also makes closing subscriber channels on
// unsubscribe and Close safe.
func (s *Store) fanOutLocked(st *site, line Line) {
	for _, sub := range st.subs {
		select {
		case sub.ch <- line:
		default:
			// Slow consumer: drop this line for this subscriber.
		}
	}
}

// closedLines returns an already-closed channel for unusable subscriptions.
func closedLines() <-chan Line {
	ch := make(chan Line)
	close(ch)
	return ch
}
