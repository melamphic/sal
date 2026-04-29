package testutil

import (
	"context"
	"strconv"
	"sync"
)

func itoa(i int) string { return strconv.Itoa(i) }

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

// SendNoteCapWarning records the 80% warning email.
func (f *FakeMailer) SendNoteCapWarning(_ context.Context, to, clinicName string, current, capLimit int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Sent = append(f.Sent, SentEmail{
		To:       to,
		Template: "note_cap_warning",
		Data: map[string]string{
			"clinic":  clinicName,
			"current": itoa(current),
			"cap":     itoa(capLimit),
		},
	})
	return nil
}

// SendNoteCapCSAlert records the 110% CS alert email.
func (f *FakeMailer) SendNoteCapCSAlert(_ context.Context, opsEmail, clinicID, clinicName string, current, capLimit int, plan string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Sent = append(f.Sent, SentEmail{
		To:       opsEmail,
		Template: "note_cap_cs_alert",
		Data: map[string]string{
			"clinic_id": clinicID,
			"clinic":    clinicName,
			"current":   itoa(current),
			"cap":       itoa(capLimit),
			"plan":      plan,
		},
	})
	return nil
}

// SendComplianceReportReady records the scheduled-report email.
func (f *FakeMailer) SendComplianceReportReady(_ context.Context, to, clinicName, reportType, periodStart, periodEnd, downloadURL string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Sent = append(f.Sent, SentEmail{
		To:       to,
		Template: "compliance_report_ready",
		Data: map[string]string{
			"clinic":       clinicName,
			"report_type":  reportType,
			"period_start": periodStart,
			"period_end":   periodEnd,
			"download_url": downloadURL,
		},
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
