package main

import (
	"context"
	"embed"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

//go:embed templates/*.html
var mailTemplates embed.FS

type billingMailer func(ctx context.Context, to, subject, templateName string, properties map[string]string) error

type billingMailTransport func(ctx context.Context, to, subject, body string) error

func billingRenderMail(templateName, subject string, properties map[string]string) (string, error) {
	base, err := mailTemplates.ReadFile("templates/Base.html")
	if err != nil {
		return "", err
	}
	content, err := mailTemplates.ReadFile("templates/" + templateName + ".html")
	if err != nil {
		return "", err
	}
	pairs := make([]string, 0, 2*len(properties))
	for key, value := range properties {
		pairs = append(pairs, "##"+strings.ToUpper(key)+"##", html.EscapeString(value))
	}
	body := strings.NewReplacer(pairs...).Replace(string(content))
	return strings.NewReplacer("##SUBJECT##", html.EscapeString(subject), "##BODY##", body).Replace(string(base)), nil
}

func (s *Server) billingMailer() billingMailer {
	transport, err := s.billingMailTransport()
	if err != nil {
		return func(context.Context, string, string, string, map[string]string) error { return err }
	}
	if transport == nil {
		return func(_ context.Context, to, subject, templateName string, _ map[string]string) error {
			slog.Info("billing email logged", "to", to, "subject", subject, "template", templateName)
			return nil
		}
	}
	return func(ctx context.Context, to, subject, templateName string, properties map[string]string) error {
		body, err := billingRenderMail(templateName, subject, properties)
		if err != nil {
			return err
		}
		return transport(ctx, to, subject, body)
	}
}

func (s *Server) billingMailTransport() (billingMailTransport, error) {
	if s.billingManagedCloud() {
		return (&billingSES{development: s.Config.Development}).send, nil
	}
	if value := os.Getenv("ConnectionStrings__mailcatcher"); value != "" {
		smtpURL, err := url.Parse(value)
		if err != nil || smtpURL.Hostname() == "" {
			return nil, fmt.Errorf("invalid ConnectionStrings__mailcatcher SMTP URL")
		}
		port := smtpURL.Port()
		if port == "" {
			port = "25"
		}
		return (billingSMTP{
			host: smtpURL.Hostname(), address: net.JoinHostPort(smtpURL.Hostname(), port), from: "hi@aptabase.com",
		}).send, nil
	}
	host := strings.TrimSpace(os.Getenv("SMTP_HOST"))
	if host == "" {
		return nil, nil
	}
	port, err := strconv.Atoi(os.Getenv("SMTP_PORT"))
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("SMTP_PORT must be between 1 and 65535")
	}
	return (billingSMTP{
		host: host, address: net.JoinHostPort(host, strconv.Itoa(port)), tls: port == 465, startTLS: port == 587,
		from: os.Getenv("SMTP_FROM_ADDRESS"), username: os.Getenv("SMTP_USERNAME"), password: os.Getenv("SMTP_PASSWORD"),
	}).send, nil
}
