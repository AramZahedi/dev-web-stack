package terminal

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(by)
}

func TestTicketIsOneUse(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	store := NewTicketStore(20*time.Second, clock.Now)

	ticket, err := store.Issue("site-1", 1000)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(ticket.Token) < 32 {
		t.Fatalf("ticket token is too short: %q", ticket.Token)
	}
	if ticket.ExpiresAt != clock.Now().Add(20*time.Second) {
		t.Fatalf("expires_at = %v, want %v", ticket.ExpiresAt, clock.Now().Add(20*time.Second))
	}

	consumed, err := store.Consume(ticket.Token, "site-1", 1000)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if consumed.SiteID != "site-1" || consumed.UID != 1000 {
		t.Fatalf("consumed ticket = %+v", consumed)
	}
	if _, err := store.Consume(ticket.Token, "site-1", 1000); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("second Consume error = %v, want ErrTicketInvalid", err)
	}
}

func TestTicketExpiresAndIsPruned(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	store := NewTicketStore(5*time.Second, clock.Now)
	ticket, err := store.Issue("site-1", 1000)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	clock.Advance(5 * time.Second)
	if _, err := store.Consume(ticket.Token, "site-1", 1000); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("Consume error = %v, want ErrTicketExpired", err)
	}
	if got := store.Len(); got != 0 {
		t.Fatalf("expired ticket retained: Len = %d", got)
	}
}

func TestTicketBindsSiteAndUID(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	store := NewTicketStore(30*time.Second, clock.Now)
	ticket, err := store.Issue("site-1", 1000)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := store.Consume(ticket.Token, "site-2", 1000); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("cross-site Consume error = %v, want ErrTicketInvalid", err)
	}
	if _, err := store.Consume(ticket.Token, "site-1", 1001); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("cross-uid Consume error = %v, want ErrTicketInvalid", err)
	}
	// A rejected attempt must not burn the ticket for its rightful owner.
	if _, err := store.Consume(ticket.Token, "site-1", 1000); err != nil {
		t.Fatalf("rightful Consume after rejected attempts: %v", err)
	}
}

func TestTicketTokensAreRandom(t *testing.T) {
	store := NewTicketStore(30*time.Second, time.Now)
	seen := make(map[string]bool)
	for i := 0; i < 32; i++ {
		ticket, err := store.Issue("site-1", 1000)
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		if seen[ticket.Token] {
			t.Fatalf("duplicate ticket token %q", ticket.Token)
		}
		if strings.ContainsAny(ticket.Token, "+/=") {
			t.Fatalf("ticket is not URL-safe: %q", ticket.Token)
		}
		seen[ticket.Token] = true
	}
}

func TestTicketConsumeIsAtomic(t *testing.T) {
	store := NewTicketStore(30*time.Second, time.Now)
	ticket, err := store.Issue("site-1", 1000)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	const goroutines = 32
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := store.Consume(ticket.Token, "site-1", 1000); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	if successes != 1 {
		t.Fatalf("successful consumes = %d, want exactly 1", successes)
	}
}

func TestTicketTTLIsCappedAtThirtySeconds(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	store := NewTicketStore(time.Hour, clock.Now)
	ticket, err := store.Issue("site-1", 1000)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if got := ticket.ExpiresAt.Sub(clock.Now()); got > 30*time.Second {
		t.Fatalf("ticket TTL = %v, want <= 30s", got)
	}
}
