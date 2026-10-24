package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
)

// Message is one outgoing mail. HTML may be empty, in which case only the text
// part is sent; a reader that cannot render HTML always gets the text part.
type Message struct {
	To      []string
	Subject string
	Text    string
	HTML    string
}

// Mailer delivers a Message.
type Mailer interface {
	Send(ctx context.Context, message Message) error
}

// consoleMailer writes the mail to the server log instead of delivering it. It
// is the default so that a checkout without mail settings still lets whoever
// runs the server read a reset link.
type consoleMailer struct{}

func (consoleMailer) Send(_ context.Context, message Message) error {
	log.Printf("mail: to=%s subject=%q\n%s", strings.Join(message.To, ", "), message.Subject, message.Text)
	return nil
}

// dummyMailer throws the mail away.
type dummyMailer struct{}

func (dummyMailer) Send(context.Context, Message) error { return nil }

// mailer is what the flows send through. It starts as the console mailer so
// that a binary nobody configured still behaves like a development build.
var mailer Mailer = consoleMailer{}

// SetMailer installs the mailer the deployment asked for. main calls it once at
// startup.
func SetMailer(m Mailer) {
	if m != nil {
		mailer = m
	}
}

// MailerFromConfig builds the mailer EMAIL_BACKEND names. The names and the
// settings behind them follow Django's email configuration.
func MailerFromConfig() (Mailer, error) {
	switch strings.ToLower(strings.TrimSpace(config.EmailBackend)) {
	case "", "console":
		return consoleMailer{}, nil
	case "dummy":
		return dummyMailer{}, nil
	case "smtp":
		return newSMTPMailer()
	default:
		return nil, fmt.Errorf("service: unknown EMAIL_BACKEND %q", config.EmailBackend)
	}
}

// ErrMailNotConfigured is returned when a mail would have to be delivered but
// the deployment gave the mailer nothing to deliver it with.
var ErrMailNotConfigured = errors.New("service: mail transport is not configured")
