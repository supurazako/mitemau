package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSMTPDelivery(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	messages := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		reader := bufio.NewReader(conn)
		fmt.Fprint(conn, "220 localhost test\r\n")
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				done <- err
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				fmt.Fprint(conn, "250 localhost\r\n")
			case strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
				fmt.Fprint(conn, "250 OK\r\n")
			case line == "DATA\r\n":
				fmt.Fprint(conn, "354 Send message\r\n")
				var message strings.Builder
				for {
					part, e := reader.ReadString('\n')
					if e != nil {
						done <- e
						return
					}
					if part == ".\r\n" {
						break
					}
					message.WriteString(part)
				}
				messages <- message.String()
				fmt.Fprint(conn, "250 accepted\r\n")
			case line == "QUIT\r\n":
				fmt.Fprint(conn, "221 bye\r\n")
				done <- nil
				return
			default:
				done <- fmt.Errorf("unexpected command")
				return
			}
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	sender := smtpSender{Host: host, Port: port, From: "noreply@mitemau.test", CodeTTL: 10 * time.Minute}
	if err := sender.Send(context.Background(), "recipient@example.test", "012345"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	message := <-messages
	if !strings.Contains(message, "012345") || !strings.Contains(message, "To: recipient@example.test\r\n") {
		t.Fatal("missing code or recipient")
	}
}
func TestSMTPRejectsHeaderInjection(t *testing.T) {
	sender := smtpSender{From: "noreply@mitemau.test"}
	if err := sender.Send(context.Background(), "a@example.test\r\nBcc: victim@example.test", "123456"); err == nil {
		t.Fatal("accepted header injection")
	}
}
func TestEmailConfigRejectsUnsafeSettings(t *testing.T) {
	t.Setenv("EMAIL_CODE_SECRET", strings.Repeat("a", 64))
	t.Setenv("SMTP_HOST", "mail.example.test")
	t.Setenv("SMTP_REQUIRE_TLS", "false")
	if _, _, err := emailConfig(); err == nil {
		t.Fatal("accepted unencrypted remote SMTP")
	}
	t.Setenv("SMTP_HOST", "127.0.0.1")
	t.Setenv("EMAIL_MAX_ATTEMPTS", "0")
	if _, _, err := emailConfig(); err == nil {
		t.Fatal("accepted zero attempts")
	}
	t.Setenv("EMAIL_MAX_ATTEMPTS", "5")
	t.Setenv("EMAIL_CODE_SECRET", "REPLACE_ME")
	if _, _, err := emailConfig(); err == nil {
		t.Fatal("accepted placeholder secret")
	}
}
