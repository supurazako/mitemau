package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

func (s *verificationServer) sourceHash(r *http.Request) ([]byte, error) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return nil, fmt.Errorf("missing remote IP")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("invalid remote IP")
	}
	// Only an explicitly trusted, loopback-connected single proxy may supply
	// the last forwarded address. That proxy must append the actual client IP.
	if s.config.TrustLoopbackProxy && ip.IsLoopback() {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			parts := strings.Split(forwarded, ",")
			ip = net.ParseIP(strings.TrimSpace(parts[len(parts)-1]))
			if ip == nil {
				return nil, fmt.Errorf("invalid forwarded IP")
			}
		}
	}
	mac := hmac.New(sha256.New, s.config.Secret)
	mac.Write([]byte("email-send-source\x00"))
	mac.Write([]byte(ip.String()))
	return mac.Sum(nil), nil
}

// The limiter reservation shares the send transaction: failed SMTP delivery or
// an email cooldown rolls it back. Lock source before email to keep lock order.
func (s *verificationServer) reserveSend(r *http.Request, tx *sql.Tx, now time.Time) (time.Duration, error) {
	hash, err := s.sourceHash(r)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO email_send_limits (source_hash,window_start,sent_count) VALUES (?,?,0) ON DUPLICATE KEY UPDATE source_hash=source_hash`, hash, now)
	if err != nil {
		return 0, err
	}
	var start time.Time
	var count int
	if err = tx.QueryRowContext(r.Context(), `SELECT window_start,sent_count FROM email_send_limits WHERE source_hash=? FOR UPDATE`, hash).Scan(&start, &count); err != nil {
		return 0, err
	}
	if !now.Before(start.Add(s.config.SendWindow)) {
		start = now
		count = 0
	}
	if count >= s.config.MaxSends {
		return start.Add(s.config.SendWindow).Sub(now), nil
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE email_send_limits SET window_start=?,sent_count=? WHERE source_hash=?`, start, count+1, hash)
	return 0, err
}

// Expiry checks always happen on reads, independently of this periodic cleanup.
func (s *verificationServer) cleanup(ctx context.Context) error {
	now := s.now()
	// Bounded batches avoid a large deletion holding locks indefinitely.
	// Keep pending rows at least through cooldown, even after code consumption.
	pendingTTL := max(s.config.CodeTTL, s.config.RegistrationTTL, s.config.ResendInterval)
	queries := []struct {
		sql    string
		cutoff time.Time
	}{
		{`DELETE FROM email_verifications WHERE last_sent_at<=? LIMIT 1000`, now.Add(-pendingTTL)},
		{`DELETE FROM registration_ceremonies WHERE expires_at<=? LIMIT 1000`, now},
		{`DELETE FROM account_sessions WHERE expires_at<=? LIMIT 1000`, now},
		{`DELETE FROM verified_registrations WHERE expires_at<=? LIMIT 1000`, now},
		{`DELETE FROM email_send_limits WHERE window_start<=? LIMIT 1000`, now.Add(-s.config.SendWindow)},
	}
	for _, q := range queries {
		if _, err := s.db.ExecContext(ctx, q.sql, q.cutoff); err != nil {
			return err
		}
	}
	return nil
}
func (s *verificationServer) runCleanup(ctx context.Context) {
	run := func() {
		work, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := s.cleanup(work); err != nil {
			log.Print("email verification cleanup failed; verify database migrations and connectivity")
		}
	}
	run()
	ticker := time.NewTicker(s.config.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
