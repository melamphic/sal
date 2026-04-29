// Package mailer defines the email sending interface and provides implementations
// for development (SMTP/Mailpit) and production (SMTP relay or Resend).
//
// The Mailer interface is the only contract the rest of the application depends on.
// Swap implementations by changing the wiring in internal/app/app.go — no other
// file needs to change.
package mailer

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"html/template"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Mailer sends transactional emails.
// All methods accept context so future implementations can respect cancellation.
type Mailer interface {
	// SendMagicLink delivers a one-time login link to the given email address.
	SendMagicLink(ctx context.Context, to, name, loginURL string) error

	// SendInvite delivers a clinic invitation to a new staff member.
	SendInvite(ctx context.Context, to, inviterName, clinicName, inviteURL string) error

	// SendNoteCapWarning notifies the clinic admin they've crossed 80%
	// of their per-period (or trial) note allowance. Fired once per
	// billing period — see notecap.Service for idempotency.
	SendNoteCapWarning(ctx context.Context, to, clinicName string, current, capLimit int) error

	// SendNoteCapCSAlert pages the ops inbox when a clinic crosses
	// 110% of its note cap. Fired once per billing period.
	SendNoteCapCSAlert(ctx context.Context, opsEmail, clinicID, clinicName string, current, capLimit int, plan string) error

	// SendComplianceReportReady delivers a compliance-report download
	// link. Used by the report-scheduling pipeline once a scheduled
	// report finishes generation. The download URL is a fresh presigned
	// URL; the email body warns it expires in an hour.
	SendComplianceReportReady(ctx context.Context, to, clinicName, reportType, periodStart, periodEnd, downloadURL string) error
}

// SMTPConfig holds the credentials for an SMTP connection.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	FromName string
}

// SMTPMailer sends email via a standard SMTP server.
// In development this points at Mailpit (localhost:1025) — all mail is
// intercepted and visible at http://localhost:8025.
// In production, point at any SMTP relay (SendGrid, Postmark, AWS SES, Resend SMTP).
type SMTPMailer struct {
	cfg SMTPConfig
}

// NewSMTP creates an SMTPMailer from the given config.
func NewSMTP(cfg SMTPConfig) *SMTPMailer {
	return &SMTPMailer{cfg: cfg}
}

// SendMagicLink sends a magic login link email.
func (m *SMTPMailer) SendMagicLink(_ context.Context, to, name, loginURL string) error {
	subject := "Your Salvia login link"
	body, err := render(magicLinkTemplate, map[string]string{
		"Name":     name,
		"LoginURL": loginURL,
	})
	if err != nil {
		return fmt.Errorf("mailer.SendMagicLink: render: %w", err)
	}
	if err := m.send(to, subject, body); err != nil {
		return fmt.Errorf("mailer.SendMagicLink: send: %w", err)
	}
	return nil
}

// SendNoteCapWarning sends the 80%-of-cap warning email to a clinic admin.
func (m *SMTPMailer) SendNoteCapWarning(_ context.Context, to, clinicName string, current, capLimit int) error {
	subject := fmt.Sprintf("Heads up: %s is approaching its monthly note limit", clinicName)
	body, err := render(noteCapWarningTemplate, map[string]string{
		"ClinicName": clinicName,
		"Current":    fmt.Sprintf("%d", current),
		"Cap":        fmt.Sprintf("%d", capLimit),
	})
	if err != nil {
		return fmt.Errorf("mailer.SendNoteCapWarning: render: %w", err)
	}
	if err := m.send(to, subject, body); err != nil {
		return fmt.Errorf("mailer.SendNoteCapWarning: send: %w", err)
	}
	return nil
}

// SendNoteCapCSAlert sends the 110%-of-cap CS notification to the ops inbox.
func (m *SMTPMailer) SendNoteCapCSAlert(_ context.Context, opsEmail, clinicID, clinicName string, current, capLimit int, plan string) error {
	subject := fmt.Sprintf("[note-cap 110%%] %s exceeded its plan", clinicName)
	body, err := render(noteCapCSAlertTemplate, map[string]string{
		"ClinicID":   clinicID,
		"ClinicName": clinicName,
		"Current":    fmt.Sprintf("%d", current),
		"Cap":        fmt.Sprintf("%d", capLimit),
		"Plan":       plan,
	})
	if err != nil {
		return fmt.Errorf("mailer.SendNoteCapCSAlert: render: %w", err)
	}
	if err := m.send(opsEmail, subject, body); err != nil {
		return fmt.Errorf("mailer.SendNoteCapCSAlert: send: %w", err)
	}
	return nil
}

// SendComplianceReportReady delivers the scheduled-report email.
func (m *SMTPMailer) SendComplianceReportReady(_ context.Context, to, clinicName, reportType, periodStart, periodEnd, downloadURL string) error {
	subject := fmt.Sprintf("Your scheduled %s for %s is ready", humaniseReportType(reportType), clinicName)
	body, err := render(complianceReportReadyTemplate, map[string]string{
		"ClinicName":   clinicName,
		"ReportType":   humaniseReportType(reportType),
		"PeriodStart":  periodStart,
		"PeriodEnd":    periodEnd,
		"DownloadURL":  downloadURL,
	})
	if err != nil {
		return fmt.Errorf("mailer.SendComplianceReportReady: render: %w", err)
	}
	if err := m.send(to, subject, body); err != nil {
		return fmt.Errorf("mailer.SendComplianceReportReady: send: %w", err)
	}
	return nil
}

// humaniseReportType — the report type slug is fine for engineers but
// reads weirdly in an email. Map the known slugs to friendly names;
// fallback strips underscores so unknown slugs still look reasonable.
func humaniseReportType(slug string) string {
	switch slug {
	case "audit_pack":
		return "Compliance Audit Pack"
	case "controlled_drugs_register":
		return "Controlled Drugs Register"
	case "evidence_pack":
		return "Evidence Pack"
	case "records_audit":
		return "Records Audit"
	case "incidents_log":
		return "Incidents Log"
	default:
		out := []rune(slug)
		for i, r := range out {
			if r == '_' {
				out[i] = ' '
			}
		}
		return string(out)
	}
}

// SendInvite sends a clinic invitation email to a new staff member.
func (m *SMTPMailer) SendInvite(_ context.Context, to, inviterName, clinicName, inviteURL string) error {
	subject := fmt.Sprintf("You've been invited to join %s on Salvia", clinicName)
	body, err := render(inviteTemplate, map[string]string{
		"InviterName": inviterName,
		"ClinicName":  clinicName,
		"InviteURL":   inviteURL,
	})
	if err != nil {
		return fmt.Errorf("mailer.SendInvite: render: %w", err)
	}
	if err := m.send(to, subject, body); err != nil {
		return fmt.Errorf("mailer.SendInvite: send: %w", err)
	}
	return nil
}

// send constructs and delivers a MIME email via SMTP.
func (m *SMTPMailer) send(to, subject, htmlBody string) error {
	from := fmt.Sprintf("%s <%s>", m.cfg.FromName, m.cfg.From)

	headers := strings.Join([]string{
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=UTF-8",
		fmt.Sprintf("From: %s", from),
		fmt.Sprintf("To: %s", to),
		fmt.Sprintf("Subject: %s", subject),
	}, "\r\n")

	msg := []byte(headers + "\r\n\r\n" + htmlBody)

	addr := fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port)

	var auth smtp.Auth
	if m.cfg.Username != "" {
		auth = smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)
	}

	// For Mailpit in dev (plain SMTP, no TLS). For production, use TLS.
	if m.cfg.Port == 465 {
		return m.sendSMTPS(addr, auth, m.cfg.From, to, msg)
	}

	if m.cfg.Port == 587 {
		return m.sendSTARTTLS(addr, auth, m.cfg.From, to, msg)
	}

	// Plain SMTP (dev/Mailpit on port 1025).
	if err := smtp.SendMail(addr, auth, m.cfg.From, []string{to}, msg); err != nil {
		return fmt.Errorf("mailer.send: send mail: %w", err)
	}
	return nil
}

const smtpDialTimeout = 15 * time.Second

// sendSMTPS handles port 465 (implicit TLS) with a dial timeout.
func (m *SMTPMailer) sendSMTPS(addr string, auth smtp.Auth, from, to string, msg []byte) error {
	tlsCfg := &tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12}
	rawConn, err := net.DialTimeout("tcp", addr, smtpDialTimeout)
	if err != nil {
		return fmt.Errorf("mailer.send: dial: %w", err)
	}
	conn := tls.Client(rawConn, tlsCfg)
	if err := conn.Handshake(); err != nil {
		rawConn.Close()
		return fmt.Errorf("mailer.send: tls handshake: %w", err)
	}

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("mailer.send: smtp client: %w", err)
	}
	defer client.Close()

	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("mailer.send: auth: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("mailer.send: mail from: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("mailer.send: rcpt to: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("mailer.send: data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("mailer.send: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mailer.send: close: %w", err)
	}
	return nil
}

// sendSTARTTLS handles port 587 with mandatory STARTTLS.
// Rejects the connection if the server does not support STARTTLS.
func (m *SMTPMailer) sendSTARTTLS(addr string, auth smtp.Auth, from, to string, msg []byte) error {
	conn, err := net.DialTimeout("tcp", addr, smtpDialTimeout)
	if err != nil {
		return fmt.Errorf("mailer.send: dial: %w", err)
	}

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("mailer.send: smtp client: %w", err)
	}
	defer client.Close()

	ok, _ := client.Extension("STARTTLS")
	if !ok {
		return fmt.Errorf("mailer.send: server does not support STARTTLS, refusing plaintext")
	}

	tlsCfg := &tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12}
	if err := client.StartTLS(tlsCfg); err != nil {
		return fmt.Errorf("mailer.send: starttls: %w", err)
	}

	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("mailer.send: auth: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("mailer.send: mail from: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("mailer.send: rcpt to: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("mailer.send: data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("mailer.send: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mailer.send: close: %w", err)
	}
	return nil
}

// render executes an HTML template with the given data map.
func render(tmpl *template.Template, data map[string]string) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render: execute: %w", err)
	}
	return buf.String(), nil
}

// ── Email Templates ────────────────────────────────────────────────────────────
// Minimal but functional HTML templates.
// Replace with a proper template system (e.g. Resend React Email) before launch.

var magicLinkTemplate = template.Must(template.New("magic_link").Parse(`<!DOCTYPE html>
<html>
<body style="font-family: sans-serif; max-width: 480px; margin: 40px auto; color: #1a1a1a;">
  <h2 style="color: #1A3B2A;">Your Salvia login link</h2>
  <p>Hi {{.Name}},</p>
  <p>Click the button below to log in to Salvia. This link expires in 15 minutes and can only be used once.</p>
  <p style="margin: 32px 0;">
    <a href="{{.LoginURL}}"
       style="background:#1A3B2A;color:#fff;padding:12px 24px;border-radius:6px;text-decoration:none;font-weight:600;">
      Log in to Salvia
    </a>
  </p>
  <p style="color:#666;font-size:13px;">If you didn't request this, you can safely ignore this email.</p>
  <hr style="border:none;border-top:1px solid #eee;margin:32px 0;">
  <p style="color:#999;font-size:12px;">Salvia · Melamphic</p>
</body>
</html>`))

var noteCapWarningTemplate = template.Must(template.New("note_cap_warning").Parse(`<!DOCTYPE html>
<html>
<body style="font-family: sans-serif; max-width: 480px; margin: 40px auto; color: #1a1a1a;">
  <h2 style="color: #1A3B2A;">{{.ClinicName}} is at 80% of its note limit</h2>
  <p>Your clinic has logged <strong>{{.Current}} of {{.Cap}}</strong> notes in the current billing period.</p>
  <p>You can keep recording without interruption. If you expect to exceed the limit this period, consider upgrading your plan to avoid temporary blocks at 150%.</p>
  <p style="color:#666;font-size:13px;">Thresholds: warn at 80%, customer-success outreach at 110%, hard block at 150%.</p>
  <hr style="border:none;border-top:1px solid #eee;margin:32px 0;">
  <p style="color:#999;font-size:12px;">Salvia · Melamphic</p>
</body>
</html>`))

var noteCapCSAlertTemplate = template.Must(template.New("note_cap_cs_alert").Parse(`<!DOCTYPE html>
<html>
<body style="font-family: monospace; max-width: 560px; margin: 40px auto; color: #1a1a1a;">
  <h2 style="color: #B65A2A;">Note-cap 110% — {{.ClinicName}}</h2>
  <p>Clinic <code>{{.ClinicID}}</code> ({{.ClinicName}}) has exceeded 110% of its monthly note allowance.</p>
  <ul>
    <li>Plan: <code>{{.Plan}}</code></li>
    <li>Notes this period: <strong>{{.Current}}</strong></li>
    <li>Plan cap: {{.Cap}}</li>
  </ul>
  <p>Reach out to discuss an upgrade before the 150% hard-block kicks in.</p>
</body>
</html>`))

var inviteTemplate = template.Must(template.New("invite").Parse(`<!DOCTYPE html>
<html>
<body style="font-family: sans-serif; max-width: 480px; margin: 40px auto; color: #1a1a1a;">
  <h2 style="color: #1A3B2A;">You've been invited to Salvia</h2>
  <p>{{.InviterName}} has invited you to join <strong>{{.ClinicName}}</strong> on Salvia.</p>
  <p>Salvia is a voice-first clinical documentation platform. Click below to set up your account.</p>
  <p style="margin: 32px 0;">
    <a href="{{.InviteURL}}"
       style="background:#1A3B2A;color:#fff;padding:12px 24px;border-radius:6px;text-decoration:none;font-weight:600;">
      Accept invitation
    </a>
  </p>
  <p style="color:#666;font-size:13px;">This invitation expires in 7 days.</p>
  <hr style="border:none;border-top:1px solid #eee;margin:32px 0;">
  <p style="color:#999;font-size:12px;">Salvia · Melamphic</p>
</body>
</html>`))

// complianceReportReadyTemplate — scheduled-report delivery. Same visual
// system as the invite template; the body call-to-action is a 1-hour
// presigned download URL minted by the caller.
var complianceReportReadyTemplate = template.Must(template.New("compliance_report_ready").Parse(`<!DOCTYPE html>
<html>
<body style="font-family: sans-serif; max-width: 520px; margin: 40px auto; color: #1a1a1a;">
  <h2 style="color: #1A3B2A;">Your scheduled {{.ReportType}} is ready</h2>
  <p>The latest <strong>{{.ReportType}}</strong> for <strong>{{.ClinicName}}</strong> has been generated and is ready to download.</p>
  <p style="color:#444;">Period: <strong>{{.PeriodStart}}</strong> &mdash; <strong>{{.PeriodEnd}}</strong></p>
  <p style="margin: 32px 0;">
    <a href="{{.DownloadURL}}"
       style="background:#1A3B2A;color:#fff;padding:12px 24px;border-radius:6px;text-decoration:none;font-weight:600;">
      Download report
    </a>
  </p>
  <p style="color:#666;font-size:13px;">This download link expires in 1 hour. If it expires, sign in to Salvia and re-download from the Reports tab — the file itself is safely stored and the URL is regenerated on demand.</p>
  <hr style="border:none;border-top:1px solid #eee;margin:32px 0;">
  <p style="color:#999;font-size:12px;">Salvia · Melamphic</p>
</body>
</html>`))
