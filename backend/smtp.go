package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

type smtpSender struct {
	Host, Port, From, Username, Password string
	RequireTLS                           bool
	CodeTTL                              time.Duration
}

func (s smtpSender) Send(ctx context.Context, to, code string) error {
	return s.send(ctx, to, code, "Mitemau email verification")
}
func (s smtpSender) SendRecovery(ctx context.Context, to, code string) error {
	return s.send(ctx, to, code, "Mitemau account recovery")
}
func (s smtpSender) send(ctx context.Context, to, code, subject string) error {
	for _, address := range []string{s.From, to} {
		parsed, err := mail.ParseAddress(address)
		if err != nil || parsed.Address != address || strings.ContainsAny(address, "\r\n") {
			return fmt.Errorf("invalid mail address")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(s.Host, s.Port))
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err = conn.SetDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err = c.StartTLS(&tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	} else if s.RequireTLS {
		return fmt.Errorf("SMTP server does not support STARTTLS")
	}
	if s.Username != "" {
		state, ok := c.TLSConnectionState()
		if !ok || !state.HandshakeComplete {
			return fmt.Errorf("SMTP credentials require TLS")
		}
		if err = c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return err
		}
	}
	if err = c.Mail(s.From); err != nil {
		return err
	}
	if err = c.Rcpt(to); err != nil {
		return err
	}
	data, err := c.Data()
	if err != nil {
		return err
	}
	message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nYour verification code is: %s\r\n\r\nThis code expires in %d minutes. If you did not request it, ignore this email.\r\n", s.From, to, subject, code, int(s.CodeTTL.Minutes()))
	if _, err = data.Write([]byte(message)); err != nil {
		return err
	}
	if err = data.Close(); err != nil {
		return err
	}
	// DATA acceptance is the delivery handoff; a failed QUIT must not revoke the code.
	_ = c.Quit()
	return nil
}
