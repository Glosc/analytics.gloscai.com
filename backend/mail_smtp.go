package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

type billingSMTP struct {
	host, address, from, username, password string
	tls, startTLS                           bool
}

func (cfg billingSMTP) send(ctx context.Context, to, subject, body string) error {
	fromAddr, err := mail.ParseAddress(cfg.from)
	if err != nil {
		return fmt.Errorf("invalid SMTP_FROM_ADDRESS: %w", err)
	}
	toAddr, err := mail.ParseAddress(to)
	if err != nil {
		return fmt.Errorf("invalid recipient: %w", err)
	}
	message, err := billingSMTPMessage(fromAddr, toAddr, subject, body)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", cfg.address)
	if err != nil {
		return err
	}
	defer raw.Close()
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := raw.SetDeadline(deadline); err != nil {
		return err
	}
	conn := raw
	tlsConfig := &tls.Config{ServerName: cfg.host, MinVersion: tls.VersionTLS12}
	if cfg.tls {
		secure := tls.Client(conn, tlsConfig)
		if err := secure.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = secure
	}
	client, err := smtp.NewClient(conn, cfg.host)
	if err != nil {
		return err
	}
	defer client.Close()
	if cfg.startTLS {
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("SMTP STARTTLS: %w", err)
		}
	}
	if cfg.username != "" {
		if err := cfg.authenticate(client); err != nil {
			return err
		}
	}
	if err := client.Mail(fromAddr.Address); err != nil {
		return err
	}
	if err := client.Rcpt(toAddr.Address); err != nil {
		return err
	}
	data, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := data.Write(message); err != nil {
		return err
	}
	if err := data.Close(); err != nil {
		return err
	}
	// The server already accepted the message, so a failed QUIT must not trigger a resend.
	_ = client.Quit()
	return nil
}

func (cfg billingSMTP) authenticate(client *smtp.Client) error {
	_, auth := client.Extension("AUTH")
	login := false
	for _, mechanism := range strings.Fields(strings.ToUpper(auth)) {
		switch mechanism {
		case "PLAIN":
			return client.Auth(smtp.PlainAuth("", cfg.username, cfg.password, cfg.host))
		case "LOGIN":
			login = true
		}
	}
	if login {
		return client.Auth(billingSMTPLogin{cfg.username, cfg.password})
	}
	return errors.New("SMTP server does not offer PLAIN or LOGIN authentication")
}

type billingSMTPLogin struct{ username, password string }

func (a billingSMTPLogin) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && server.Name != "localhost" && !net.ParseIP(server.Name).IsLoopback() {
		return "", nil, errors.New("refusing SMTP authentication without TLS")
	}
	return "LOGIN", nil, nil
}

func (a billingSMTPLogin) Next(challenge []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(challenge))) {
	case "username:":
		return []byte(a.username), nil
	case "password:":
		return []byte(a.password), nil
	default:
		return nil, errors.New("unrecognized SMTP LOGIN challenge")
	}
}

func billingSMTPMessage(from, to *mail.Address, subject, body string) ([]byte, error) {
	if strings.ContainsAny(subject, "\r\n") {
		return nil, errors.New("invalid mail subject")
	}
	var message bytes.Buffer
	fmt.Fprintf(&message, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\n",
		from, to, mime.QEncoding.Encode("UTF-8", subject), time.Now().Format(time.RFC1123Z))
	message.WriteString("MIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	writer := quotedprintable.NewWriter(&message)
	if _, err := writer.Write([]byte(body)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return message.Bytes(), nil
}
