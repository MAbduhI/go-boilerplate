// Package mailpit sends mail through Mailpit, the development mail catcher.
//
// Mailpit speaks plain SMTP, so net/smtp from the standard library is enough and
// no dependency is added. The same code points at any SMTP relay, which is why
// TLS and authentication are supported even though a local Mailpit needs
// neither.
package mailpit

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/MAbduhI/go-boilerplate/shared/config"
	"github.com/MAbduhI/go-boilerplate/shared/logger"
)

// Config describes the SMTP endpoint.
type Config struct {
	Host string
	Port string
	From string

	// Username and Password are empty for a local Mailpit, which accepts mail
	// unauthenticated.
	Username string
	Password string

	// UseTLS wraps the connection in TLS from the start (SMTPS, usually :465).
	UseTLS bool
	// UseSTARTTLS upgrades a plaintext connection (usually :587).
	UseSTARTTLS bool
	// InsecureSkipVerify accepts a self-signed certificate. Never enable it
	// against a real relay.
	InsecureSkipVerify bool

	Timeout time.Duration
}

// LoadConfig reads Config from MAILPIT_* environment variables.
func LoadConfig() Config {
	return Config{
		Host: config.String("MAILPIT_HOST", "localhost"),
		Port: config.String("MAILPIT_PORT", "1025"),
		From: config.String("MAILPIT_FROM", "no-reply@localhost"),

		Username: config.String("MAILPIT_USERNAME", ""),
		Password: config.String("MAILPIT_PASSWORD", ""),

		UseTLS:             config.Bool("MAILPIT_USE_TLS", false),
		UseSTARTTLS:        config.Bool("MAILPIT_USE_STARTTLS", false),
		InsecureSkipVerify: config.Bool("MAILPIT_INSECURE_SKIP_VERIFY", false),

		Timeout: config.Duration("MAILPIT_TIMEOUT", 10*time.Second),
	}
}

// Addr returns host:port.
func (c Config) Addr() string { return net.JoinHostPort(c.Host, c.Port) }

// Message is one outbound email.
type Message struct {
	From    string // defaults to Config.From
	To      []string
	CC      []string
	BCC     []string
	Subject string

	// TextBody and HTMLBody may both be set, producing a multipart/alternative
	// message so a client that cannot render HTML still shows the text part.
	TextBody string
	HTMLBody string

	ReplyTo string
	Headers map[string]string
}

// Mailer sends mail.
type Mailer struct {
	cfg Config
	log *slog.Logger
}

// New returns a Mailer. It does not dial: SMTP connections are per-message, so
// there is nothing to hold open.
func New(cfg Config) *Mailer {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	return &Mailer{cfg: cfg, log: logger.Module("mailpit")}
}

// Config reports the resolved configuration.
func (m *Mailer) Config() Config { return m.cfg }

// Send delivers msg.
func (m *Mailer) Send(ctx context.Context, msg Message) error {
	if len(msg.To) == 0 {
		return errors.New("mailpit: message has no recipient")
	}
	from := msg.From
	if from == "" {
		from = m.cfg.From
	}
	if from == "" {
		return errors.New("mailpit: no sender; set MAILPIT_FROM or Message.From")
	}

	body := render(from, msg)

	// BCC recipients receive the message but must not appear in the headers,
	// which is why the envelope recipient list is built separately from render.
	recipients := make([]string, 0, len(msg.To)+len(msg.CC)+len(msg.BCC))
	recipients = append(recipients, msg.To...)
	recipients = append(recipients, msg.CC...)
	recipients = append(recipients, msg.BCC...)

	start := time.Now()
	if err := m.deliver(ctx, from, recipients, body); err != nil {
		return err
	}

	m.log.Info("mail sent",
		slog.String("to", strings.Join(msg.To, ",")),
		slog.String("subject", msg.Subject),
		slog.Duration("took", time.Since(start)))
	return nil
}

// deliver runs the SMTP conversation, honouring ctx cancellation.
func (m *Mailer) deliver(ctx context.Context, from string, to []string, body []byte) error {
	// net/smtp has no context-aware API, so the dial is done here with a
	// deadline and the conversation runs on the resulting connection. Doing it
	// this way is what lets a shutdown actually interrupt a send.
	dialer := &net.Dialer{Timeout: m.cfg.Timeout}
	addr := m.cfg.Addr()

	var (
		conn net.Conn
		err  error
	)
	if m.cfg.UseTLS {
		// tls.Dialer rather than tls.DialWithDialer: only the former honours ctx,
		// so a shutdown can interrupt the handshake as well as the TCP connect.
		tlsDialer := &tls.Dialer{NetDialer: dialer, Config: m.tlsConfig()}
		conn, err = tlsDialer.DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("mailpit: dial %s: %w", addr, err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(m.cfg.Timeout))
	}

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return fmt.Errorf("mailpit: smtp handshake with %s: %w", addr, err)
	}
	defer client.Close()

	if m.cfg.UseSTARTTLS && !m.cfg.UseTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(m.tlsConfig()); err != nil {
				return fmt.Errorf("mailpit: starttls: %w", err)
			}
		}
	}

	if m.cfg.Username != "" {
		auth := smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("mailpit: auth: %w", err)
		}
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("mailpit: MAIL FROM %s: %w", from, err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("mailpit: RCPT TO %s: %w", rcpt, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("mailpit: DATA: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("mailpit: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mailpit: close body: %w", err)
	}
	return client.Quit()
}

func (m *Mailer) tlsConfig() *tls.Config {
	return &tls.Config{
		ServerName:         m.cfg.Host,
		InsecureSkipVerify: m.cfg.InsecureSkipVerify, //nolint:gosec // opt-in via MAILPIT_INSECURE_SKIP_VERIFY
		MinVersion:         tls.VersionTLS12,
	}
}

// Ping checks the SMTP port is reachable, for a health check.
func (m *Mailer) Ping(ctx context.Context) error {
	dialer := &net.Dialer{Timeout: m.cfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", m.cfg.Addr())
	if err != nil {
		return fmt.Errorf("mailpit: ping %s: %w", m.cfg.Addr(), err)
	}
	return conn.Close()
}

// render builds RFC 5322 message bytes.
func render(from string, msg Message) []byte {
	var b strings.Builder

	writeHeader(&b, "From", from)
	writeHeader(&b, "To", strings.Join(msg.To, ", "))
	if len(msg.CC) > 0 {
		writeHeader(&b, "Cc", strings.Join(msg.CC, ", "))
	}
	if msg.ReplyTo != "" {
		writeHeader(&b, "Reply-To", msg.ReplyTo)
	}
	// Encode the subject so non-ASCII characters survive; a raw UTF-8 subject
	// line is not valid in a mail header.
	writeHeader(&b, "Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	writeHeader(&b, "Date", time.Now().Format(time.RFC1123Z))
	writeHeader(&b, "MIME-Version", "1.0")

	for k, v := range msg.Headers {
		writeHeader(&b, k, v)
	}

	switch {
	case msg.HTMLBody != "" && msg.TextBody != "":
		boundary := fmt.Sprintf("boundary-%d", time.Now().UnixNano())
		writeHeader(&b, "Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
		b.WriteString("\r\n")

		writePart(&b, boundary, "text/plain; charset=utf-8", msg.TextBody)
		writePart(&b, boundary, "text/html; charset=utf-8", msg.HTMLBody)
		b.WriteString("--" + boundary + "--\r\n")

	case msg.HTMLBody != "":
		writeHeader(&b, "Content-Type", "text/html; charset=utf-8")
		b.WriteString("\r\n")
		b.WriteString(normalizeNewlines(msg.HTMLBody))

	default:
		writeHeader(&b, "Content-Type", "text/plain; charset=utf-8")
		b.WriteString("\r\n")
		b.WriteString(normalizeNewlines(msg.TextBody))
	}

	return []byte(b.String())
}

func writeHeader(b *strings.Builder, key, value string) {
	// Strip CR and LF from header values: an unfiltered newline lets a caller
	// inject arbitrary headers or a second message body.
	value = strings.NewReplacer("\r", "", "\n", "").Replace(value)
	fmt.Fprintf(b, "%s: %s\r\n", key, value)
}

func writePart(b *strings.Builder, boundary, contentType, body string) {
	b.WriteString("--" + boundary + "\r\n")
	fmt.Fprintf(b, "Content-Type: %s\r\n\r\n", contentType)
	b.WriteString(normalizeNewlines(body))
	b.WriteString("\r\n")
}

// normalizeNewlines converts bare LF to CRLF, which SMTP requires.
func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}
