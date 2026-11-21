package service

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"rqark-data-cloud/internal/config"
)

// fakeSMTP is a one-connection SMTP server: enough of the dialogue for net/smtp
// to finish a delivery, so a test can read what would have gone on the wire.
type fakeSMTP struct {
	listener net.Listener
	data     chan string
	auth     chan string
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &fakeSMTP{listener: listener, data: make(chan string, 1), auth: make(chan string, 1)}
	go server.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

func (f *fakeSMTP) serve() {
	conn, err := f.listener.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	send := func(line string) {
		_, _ = writer.WriteString(line + "\r\n")
		_ = writer.Flush()
	}

	send("220 fake ESMTP")

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)

		switch {
		case strings.HasPrefix(upper, "EHLO"):
			_, _ = writer.WriteString("250-fake\r\n250-AUTH PLAIN\r\n250 OK\r\n")
			_ = writer.Flush()
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			select {
			case f.auth <- line:
			default:
			}
			send("235 2.7.0 Authentication successful")
		case strings.HasPrefix(upper, "MAIL FROM"), strings.HasPrefix(upper, "RCPT TO"):
			send("250 OK")
		case upper == "DATA":
			send("354 End data with <CR><LF>.<CR><LF>")
			var body strings.Builder
			for {
				chunk, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if chunk == ".\r\n" {
					break
				}
				body.WriteString(chunk)
			}
			select {
			case f.data <- body.String():
			default:
			}
			send("250 OK")
		case upper == "QUIT":
			send("221 Bye")
			return
		default:
			send("250 OK")
		}
	}
}

// useFakeSMTP points the mail settings at the fake server and restores them
// afterwards.
func useFakeSMTP(t *testing.T, server *fakeSMTP, withAuth bool) {
	t.Helper()

	_, portText, err := net.SplitHostPort(server.listener.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	previousHost, previousPort := config.EmailHost, config.EmailPort
	previousUser, previousPassword := config.EmailHostUser, config.EmailHostPassword
	previousFrom, previousTLS, previousSSL := config.DefaultFromEmail, config.EmailUseTLS, config.EmailUseSSL
	t.Cleanup(func() {
		config.EmailHost, config.EmailPort = previousHost, previousPort
		config.EmailHostUser, config.EmailHostPassword = previousUser, previousPassword
		config.DefaultFromEmail, config.EmailUseTLS, config.EmailUseSSL = previousFrom, previousTLS, previousSSL
	})

	config.EmailHost = "127.0.0.1"
	config.EmailPort = port
	config.DefaultFromEmail = "noreply@example.com"
	config.EmailUseTLS = false
	config.EmailUseSSL = false
	if withAuth {
		config.EmailHostUser = "mailer"
		config.EmailHostPassword = "secret"
	} else {
		config.EmailHostUser = ""
		config.EmailHostPassword = ""
	}
}

// TestSMTPMailerDelivers covers a whole delivery against a real socket: the
// envelope, the authentication and the multipart body all have to survive the
// dialogue net/smtp runs.
func TestSMTPMailerDelivers(t *testing.T) {
	server := startFakeSMTP(t)
	useFakeSMTP(t, server, true)

	mailer, err := newSMTPMailer()
	if err != nil {
		t.Fatalf("newSMTPMailer: %v", err)
	}

	message := Message{
		To:      []string{"boss@example.com"},
		Subject: "Password reset for rqark-data-cloud",
		Text:    "open the link",
		HTML:    "<p>open the link</p>",
	}
	if err := mailer.Send(context.Background(), message); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case line := <-server.auth:
		if !strings.Contains(line, "AUTH PLAIN") {
			t.Errorf("auth line = %q, want AUTH PLAIN", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the mailer never authenticated")
	}

	select {
	case body := <-server.data:
		for _, want := range []string{"From: noreply@example.com", "To: boss@example.com", "Subject: Password reset for rqark-data-cloud", "multipart/alternative", "open the link", "<p>open the link</p>"} {
			if !strings.Contains(body, want) {
				t.Errorf("message does not contain %q:\n%s", want, body)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the mailer never sent a body")
	}
}

// TestSMTPMailerRequiresStartTLS covers EMAIL_USE_TLS against a server that does
// not offer it: the delivery must fail rather than fall back to plain text.
func TestSMTPMailerRequiresStartTLS(t *testing.T) {
	server := startFakeSMTP(t)
	useFakeSMTP(t, server, false)
	config.EmailUseTLS = true

	mailer, err := newSMTPMailer()
	if err != nil {
		t.Fatalf("newSMTPMailer: %v", err)
	}

	err = mailer.Send(context.Background(), Message{To: []string{"boss@example.com"}, Subject: "x", Text: "x"})
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("error = %v, want a STARTTLS failure", err)
	}
}

// TestMailerFromConfig covers the settings a deployment can pick and the
// combinations that cannot work.
func TestMailerFromConfig(t *testing.T) {
	previousBackend, previousHost := config.EmailBackend, config.EmailHost
	previousTLS, previousSSL := config.EmailUseTLS, config.EmailUseSSL
	t.Cleanup(func() {
		config.EmailBackend, config.EmailHost = previousBackend, previousHost
		config.EmailUseTLS, config.EmailUseSSL = previousTLS, previousSSL
	})

	config.EmailHost = ""
	config.EmailUseTLS = false
	config.EmailUseSSL = false

	config.EmailBackend = ""
	if m, err := MailerFromConfig(); err != nil {
		t.Errorf("an unset backend: %v", err)
	} else if _, ok := m.(consoleMailer); !ok {
		t.Errorf("an unset backend = %T, want the console mailer", m)
	}

	config.EmailBackend = "console"
	if m, err := MailerFromConfig(); err != nil {
		t.Errorf("console: %v", err)
	} else if _, ok := m.(consoleMailer); !ok {
		t.Errorf("console = %T, want the console mailer", m)
	}

	config.EmailBackend = "dummy"
	if m, err := MailerFromConfig(); err != nil {
		t.Errorf("dummy: %v", err)
	} else if _, ok := m.(dummyMailer); !ok {
		t.Errorf("dummy = %T, want the dummy mailer", m)
	}

	config.EmailBackend = "smtp"
	if _, err := MailerFromConfig(); err == nil {
		t.Error("smtp without EMAIL_HOST should be refused")
	}

	config.EmailHost = "mail.example.com"
	if _, err := MailerFromConfig(); err != nil {
		t.Errorf("smtp with a host: %v", err)
	}

	config.EmailUseTLS = true
	config.EmailUseSSL = true
	if _, err := MailerFromConfig(); err == nil {
		t.Error("EMAIL_USE_TLS together with EMAIL_USE_SSL should be refused")
	}

	config.EmailBackend = "carrier-pigeon"
	if _, err := MailerFromConfig(); err == nil {
		t.Error("an unknown backend should be refused")
	}
}

// TestBuildMessage covers the two shapes a message can take and the encoding a
// non-ASCII subject needs.
func TestBuildMessage(t *testing.T) {
	text := buildMessage("noreply@example.com", Message{To: []string{"boss@example.com"}, Subject: "hello", Text: "plain body"})
	if !strings.Contains(string(text), "Content-Type: text/plain") {
		t.Errorf("a text-only message is not text/plain:\n%s", text)
	}
	if strings.Contains(string(text), "multipart") {
		t.Errorf("a text-only message should not be multipart:\n%s", text)
	}

	both := string(buildMessage("noreply@example.com", Message{To: []string{"boss@example.com"}, Subject: "hello", Text: "plain body", HTML: "<p>html body</p>"}))
	if !strings.Contains(both, "multipart/alternative") {
		t.Errorf("a message with HTML is not multipart:\n%s", both)
	}
	for _, want := range []string{"Content-Type: text/plain; charset=utf-8", "Content-Type: text/html; charset=utf-8", "plain body", "<p>html body</p>"} {
		if !strings.Contains(both, want) {
			t.Errorf("message does not contain %q:\n%s", want, both)
		}
	}

	chinese := string(buildMessage("noreply@example.com", Message{To: []string{"boss@example.com"}, Subject: "重設密碼", Text: "重設連結"}))
	if !strings.Contains(chinese, "Subject: =?utf-8?q?") {
		t.Errorf("a non-ASCII subject is not encoded:\n%s", chinese)
	}
	if !strings.Contains(chinese, "Content-Transfer-Encoding: base64") {
		t.Errorf("a non-ASCII body is not base64:\n%s", chinese)
	}
}

// TestPasswordResetMessage covers the mail the reset flow sends: both parts
// carry the link, and the subject names the deployment.
func TestPasswordResetMessage(t *testing.T) {
	message, err := passwordResetMessage("boss@example.com", "boss", "rqark-data-cloud", "https://example.com/en/reset-password?token=abc", time.Hour)
	if err != nil {
		t.Fatalf("passwordResetMessage: %v", err)
	}

	if len(message.To) != 1 || message.To[0] != "boss@example.com" {
		t.Errorf("To = %v", message.To)
	}
	if message.Subject != "Password reset for rqark-data-cloud" {
		t.Errorf("Subject = %q", message.Subject)
	}
	for name, part := range map[string]string{"text": message.Text, "html": message.HTML} {
		for _, want := range []string{"https://example.com/en/reset-password?token=abc", "boss", "rqark-data-cloud", "1 hour"} {
			if !strings.Contains(part, want) {
				t.Errorf("the %s part does not contain %q:\n%s", name, want, part)
			}
		}
	}
}

// TestHumanDuration covers the wording the template uses for the token life.
func TestHumanDuration(t *testing.T) {
	cases := map[time.Duration]string{
		time.Hour:               "1 hour",
		90 * time.Minute:        "90 minutes",
		2 * time.Hour:           "2 hours",
		30 * time.Minute:        "30 minutes",
		time.Minute:             "1 minute",
		90 * time.Second:        "1 minute",
		25 * time.Hour:          "25 hours",
		1500 * time.Millisecond: "less than a minute",
		0:                       "less than a minute",
	}

	for d, want := range cases {
		if got := humanDuration(d); got != want {
			t.Errorf("humanDuration(%s) = %q, want %q", d, got, want)
		}
	}
}
