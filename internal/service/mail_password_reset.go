package service

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	"strings"
	texttemplate "text/template"
	"time"
)

//go:embed templates/password_reset.txt templates/password_reset.html
var passwordResetTemplates embed.FS

// passwordResetData is what the reset mail templates are given.
type passwordResetData struct {
	// Project names the deployment in the mail, so a reader who uses more than
	// one knows which account this is about.
	Project   string
	Username  string
	Link      string
	ExpiresIn string
}

// passwordResetMessage renders the mail that carries a reset link. The text and
// the HTML part share one set of data, so they can never disagree about the
// link.
func passwordResetMessage(to, username, project, link string, ttl time.Duration) (Message, error) {
	data := passwordResetData{
		Project:   project,
		Username:  username,
		Link:      link,
		ExpiresIn: humanDuration(ttl),
	}

	text, err := renderTemplate("templates/password_reset.txt", data)
	if err != nil {
		return Message{}, err
	}
	html, err := renderTemplate("templates/password_reset.html", data)
	if err != nil {
		return Message{}, err
	}

	return Message{
		To:      []string{to},
		Subject: "Password reset for " + data.Project,
		Text:    text,
		HTML:    html,
	}, nil
}

// renderTemplate fills one embedded template. The .html file is parsed as HTML
// so that the link is escaped for its attribute, everything else as text.
func renderTemplate(name string, data passwordResetData) (string, error) {
	raw, err := passwordResetTemplates.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("service: read template %s: %w", name, err)
	}

	var buf bytes.Buffer
	if strings.HasSuffix(name, ".html") {
		tmpl, err := htmltemplate.New(name).Parse(string(raw))
		if err != nil {
			return "", fmt.Errorf("service: parse template %s: %w", name, err)
		}
		if err := tmpl.Execute(&buf, data); err != nil {
			return "", fmt.Errorf("service: render template %s: %w", name, err)
		}
		return buf.String(), nil
	}

	tmpl, err := texttemplate.New(name).Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("service: parse template %s: %w", name, err)
	}
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("service: render template %s: %w", name, err)
	}
	return buf.String(), nil
}

// humanDuration says how long a token lasts in the units a reader expects: a
// short life in minutes, a longer one in hours.
func humanDuration(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	}
	minutes := int(d.Minutes())
	if minutes == 0 {
		return "less than a minute"
	}
	if minutes == 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", minutes)
}
