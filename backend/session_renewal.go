package main

import (
	"database/sql"
	"net/http"
	"time"
)

func (p *passkeyServer) renewAfterSeconds() int {
	return max(1, int(min(30*time.Minute, p.sessionTTL/2).Seconds()))
}

// Renewal requires a still-valid session; it never moves the absolute deadline.
// GET /session is deliberately read-only, so background status checks cannot
// keep a session alive. The caller signals activity explicitly with this POST.
func (p *passkeyServer) renewSession(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if !decodeBody(w, r, &input) {
		return
	}
	token := cookieToken(r, sessionCookie)
	if token == "" {
		problem(w, 401, "Sign in to continue")
		return
	}
	tx, err := p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Session renewal is temporarily unavailable")
		return
	}
	defer tx.Rollback()
	var accountID []byte
	var idle, absolute time.Time
	err = tx.QueryRowContext(r.Context(), `SELECT account_id,expires_at,absolute_expires_at FROM account_sessions WHERE token_hash=? FOR UPDATE`, tokenHash(token)).Scan(&accountID, &idle, &absolute)
	if err == sql.ErrNoRows {
		p.writeCookie(w, sessionCookie, "", -1)
		problem(w, 401, "Your session expired. Please sign in again")
		return
	}
	if err != nil {
		problem(w, 503, "Session renewal is temporarily unavailable")
		return
	}
	now := p.verification.now()
	if !now.Before(idle) || !now.Before(absolute) {
		if _, err = tx.ExecContext(r.Context(), `DELETE FROM account_sessions WHERE token_hash=?`, tokenHash(token)); err != nil {
			problem(w, 503, "Session renewal is temporarily unavailable")
			return
		}
		if err = tx.Commit(); err != nil {
			problem(w, 503, "Session renewal is temporarily unavailable")
			return
		}
		p.writeCookie(w, sessionCookie, "", -1)
		problem(w, 401, "Your session expired. Please sign in again")
		return
	}
	var email string
	if err = tx.QueryRowContext(r.Context(), `SELECT email FROM accounts WHERE id=?`, accountID).Scan(&email); err != nil {
		problem(w, 503, "Session renewal is temporarily unavailable")
		return
	}
	expires := minTime(now.Add(p.sessionTTL), absolute)
	// Do not shorten an existing deadline if an operator reduces the idle setting.
	expires = minTime(maxTime(idle, expires), absolute)
	if _, err = tx.ExecContext(r.Context(), `UPDATE account_sessions SET expires_at=? WHERE token_hash=?`, expires, tokenHash(token)); err != nil {
		problem(w, 503, "Session renewal is temporarily unavailable")
		return
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Session renewal is temporarily unavailable")
		return
	}
	p.writeCookie(w, sessionCookie, token, max(1, int(expires.Sub(now).Seconds())))
	writeJSON(w, 200, map[string]any{"signedIn": true, "email": email, "expiresAt": expires, "renewAfterSeconds": p.renewAfterSeconds()})
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
