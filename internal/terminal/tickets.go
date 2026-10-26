package terminal

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// TicketStore holds hashed, short-lived, one-use terminal tickets. Entries are
// keyed by the SHA-256 of the token, so a heap dump or log cannot replay the
// presented secret.
type TicketStore struct {
	ttl time.Duration
	now func() time.Time

	mu      sync.Mutex
	entries map[[sha256.Size]byte]ticketEntry
}

type ticketEntry struct {
	siteID    string
	uid       int
	expiresAt time.Time
}

// NewTicketStore returns an empty store. A non-positive or over-long TTL is
// capped at MaxTicketTTL.
func NewTicketStore(ttl time.Duration, now func() time.Time) *TicketStore {
	if ttl <= 0 || ttl > MaxTicketTTL {
		ttl = DefaultTicketTTL
	}
	if now == nil {
		now = time.Now
	}
	return &TicketStore{
		ttl:     ttl,
		now:     now,
		entries: make(map[[sha256.Size]byte]ticketEntry),
	}
}

// Issue creates a cryptographically random URL-safe ticket bound to one site
// and UID. Expired entries are pruned on every issue.
func (s *TicketStore) Issue(siteID string, uid int) (Ticket, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Ticket{}, fmt.Errorf("terminal: generate ticket: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	now := s.now()
	expires := now.Add(s.ttl)
	entry := ticketEntry{siteID: siteID, uid: uid, expiresAt: expires}

	s.mu.Lock()
	s.pruneLocked(now)
	s.entries[sha256.Sum256([]byte(token))] = entry
	s.mu.Unlock()

	return Ticket{Token: token, ExpiresAt: expires, SiteID: siteID, UID: uid}, nil
}

// Consume atomically validates and removes a ticket. A mismatched site or UID
// does not burn the ticket. Expired and unknown tickets are indistinguishable
// to callers except for the stable error code.
func (s *TicketStore) Consume(token, siteID string, uid int) (Ticket, error) {
	if token == "" {
		return Ticket{}, ErrTicketInvalid
	}
	key := sha256.Sum256([]byte(token))
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok {
		return Ticket{}, ErrTicketInvalid
	}
	if !now.Before(entry.expiresAt) {
		delete(s.entries, key)
		return Ticket{}, ErrTicketExpired
	}
	if entry.siteID != siteID || entry.uid != uid {
		return Ticket{}, ErrTicketInvalid
	}
	delete(s.entries, key)
	return Ticket{
		Token:     token,
		ExpiresAt: entry.expiresAt,
		SiteID:    entry.siteID,
		UID:       entry.uid,
	}, nil
}

// Len reports the number of retained tickets, including expired entries not
// yet pruned.
func (s *TicketStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

func (s *TicketStore) pruneLocked(now time.Time) {
	for key, entry := range s.entries {
		if !now.Before(entry.expiresAt) {
			delete(s.entries, key)
		}
	}
}

func randomID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
