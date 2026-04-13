package testutil

import (
	"context"
	"sync"
)

// FakeMailer records emails sent during a test without making network calls.
// All methods are safe for concurrent use.
type FakeMailer struct {
	mu   sync.Mutex
	Sent []SentEmail
}

// SentEmail records the details of a single sent email.
type SentEmail struct {
	To       string
	Template string // "magic_link" | "invite"
	Data     map[string]string
}

// SendMagicLink records a magic link email.
func (f *FakeMailer) SendMagicLink(_ context.Context, to, name, loginURL string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Sent = append(f.Sent, SentEmail{
		To:       to,
		Template: "magic_link",
		Data:     map[string]string{"name": name, "login_url": loginURL},
	})
	return nil
}

// SendInvite records an invite email.
func (f *FakeMailer) SendInvite(_ context.Context, to, inviterName, clinicName, inviteURL string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Sent = append(f.Sent, SentEmail{
		To:       to,
		Template: "invite",
		Data:     map[string]string{"inviter": inviterName, "clinic": clinicName, "url": inviteURL},
	})
	return nil
}

// Count returns the number of emails sent matching the given template.
func (f *FakeMailer) Count(template string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, e := range f.Sent {
		if e.Template == template {
			n++
		}
	}
	return n
}

// Last returns the most recently sent email, or nil if none.
func (f *FakeMailer) Last() *SentEmail {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.Sent) == 0 {
		return nil
	}
	e := f.Sent[len(f.Sent)-1]
	return &e
}

// Reset clears all recorded emails.
func (f *FakeMailer) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Sent = nil
}
