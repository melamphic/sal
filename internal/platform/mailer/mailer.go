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
	"net/smtp"
	"strings"
)

// Mailer sends transactional emails.
// All methods accept context so future implementations can respect cancellation.
type Mailer interface {
	// SendMagicLink delivers a one-time login link to the given email address.
	SendMagicLink(ctx context.Context, to, name, loginURL string) error

	// SendInvite delivers a clinic invitation to a new staff member.
	SendInvite(ctx context.Context, to, inviterName, clinicName, inviteURL string) error
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
		// TLS from the start (SMTPS).
		tlsCfg := &tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12}
		conn, err := tls.Dial("tcp", addr, tlsCfg)
		if err != nil {
			return fmt.Errorf("mailer.send: tls dial: %w", err)
		}
		defer conn.Close()

		client, err := smtp.NewClient(conn, m.cfg.Host)
		if err != nil {
			return fmt.Errorf("mailer.send: smtp client: %w", err)
		}
		defer client.Close()

		if auth != nil {
			if err := client.Auth(auth); err != nil {
				return fmt.Errorf("mailer.send: auth: %w", err)
			}
		}
		if err := client.Mail(m.cfg.From); err != nil {
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

	// Plain SMTP (dev/Mailpit) or STARTTLS (port 587).
	if err := smtp.SendMail(addr, auth, m.cfg.From, []string{to}, msg); err != nil {
		return fmt.Errorf("mailer.send: send mail: %w", err)
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
