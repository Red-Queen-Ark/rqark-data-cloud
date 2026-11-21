package service

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"rqark-data-cloud/internal/config"
)

// smtpMailer delivers over SMTP. EMAIL_USE_TLS is STARTTLS on a plain
// connection (port 587); EMAIL_USE_SSL is TLS from the first byte (port 465).
// They cannot both be set.
type smtpMailer struct {
	host     string
	port     int
	username string
	password string
	from     string
	useTLS   bool
	useSSL   bool
	timeout  time.Duration
}

func newSMTPMailer() (smtpMailer, error) {
	if config.EmailHost == "" {
		return smtpMailer{}, fmt.Errorf("service: EMAIL_HOST is required when EMAIL_BACKEND is smtp")
	}
	if config.EmailUseTLS && config.EmailUseSSL {
		return smtpMailer{}, fmt.Errorf("service: EMAIL_USE_TLS and EMAIL_USE_SSL are mutually exclusive")
	}
	return smtpMailer{
		host:     config.EmailHost,
		port:     config.EmailPort,
		username: config.EmailHostUser,
		password: config.EmailHostPassword,
		from:     config.DefaultFromEmail,
		useTLS:   config.EmailUseTLS,
		useSSL:   config.EmailUseSSL,
		timeout:  config.EmailTimeout,
	}, nil
}

func (m smtpMailer) Send(ctx context.Context, message Message) error {
	if len(message.To) == 0 {
		return fmt.Errorf("service: mail has no recipient")
	}

	addr := net.JoinHostPort(m.host, strconv.Itoa(m.port))
	dialer := &net.Dialer{Timeout: m.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("service: dial %s: %w", addr, err)
	}
	if m.timeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(m.timeout))
	}

	if m.useSSL {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: m.host})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return fmt.Errorf("service: tls handshake with %s: %w", addr, err)
		}
		conn = tlsConn
	}

	client, err := smtp.NewClient(conn, m.host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("service: smtp client: %w", err)
	}
	defer func() { _ = client.Close() }()

	if m.useTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("service: %s does not offer STARTTLS", addr)
		}
		if err := client.StartTLS(&tls.Config{ServerName: m.host}); err != nil {
			return fmt.Errorf("service: starttls: %w", err)
		}
	}

	if m.username != "" {
		if err := client.Auth(smtp.PlainAuth("", m.username, m.password, m.host)); err != nil {
			return fmt.Errorf("service: smtp auth: %w", err)
		}
	}

	if err := client.Mail(m.from); err != nil {
		return fmt.Errorf("service: mail from: %w", err)
	}
	for _, to := range message.To {
		if err := client.Rcpt(to); err != nil {
			return fmt.Errorf("service: rcpt %s: %w", to, err)
		}
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("service: data: %w", err)
	}
	if _, err := writer.Write(buildMessage(m.from, message)); err != nil {
		_ = writer.Close()
		return fmt.Errorf("service: write message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("service: close message: %w", err)
	}

	return client.Quit()
}

// buildMessage renders the headers and body of one mail. The text part always
// goes out; HTML joins it as the preferred alternative when it is present.
func buildMessage(from string, message Message) []byte {
	var b strings.Builder

	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(message.To, ", ") + "\r\n")
	b.WriteString("Subject: " + encodeHeader(message.Subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")

	if message.HTML == "" {
		encoding, body := encodeBody(message.Text)
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		b.WriteString("Content-Transfer-Encoding: " + encoding + "\r\n\r\n")
		b.WriteString(body)
		return []byte(b.String())
	}

	boundary := newBoundary()
	b.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")

	writePart(&b, boundary, "text/plain", message.Text)
	writePart(&b, boundary, "text/html", message.HTML)
	b.WriteString("--" + boundary + "--\r\n")

	return []byte(b.String())
}

func writePart(b *strings.Builder, boundary, contentType, body string) {
	encoding, encoded := encodeBody(body)
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: " + contentType + "; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: " + encoding + "\r\n\r\n")
	b.WriteString(encoded + "\r\n")
}

// encodeHeader leaves an ASCII subject alone and RFC 2047 encodes the rest, so
// a subject written in Chinese survives a mail server that assumes ASCII.
func encodeHeader(value string) string {
	for _, r := range value {
		if r > 127 {
			return mime.QEncoding.Encode("utf-8", value)
		}
	}
	return value
}

// encodeBody sends ASCII as it stands and base64 encodes anything else, which
// keeps the body byte-exact without depending on the server accepting 8-bit
// data.
func encodeBody(body string) (string, string) {
	for _, r := range body {
		if r > 127 {
			return "base64", wrap(base64.StdEncoding.EncodeToString([]byte(body)))
		}
	}
	return "7bit", body
}

// wrap breaks an encoded body into lines of at most 76 characters, as RFC 2045
// asks of base64.
func wrap(encoded string) string {
	const width = 76
	var b strings.Builder
	for len(encoded) > width {
		b.WriteString(encoded[:width] + "\r\n")
		encoded = encoded[width:]
	}
	b.WriteString(encoded)
	return b.String()
}

func newBoundary() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "rqark-mail-boundary"
	}
	return "rqark-" + hex.EncodeToString(buf)
}
