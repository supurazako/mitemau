package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

const recoveryCookie = "mitemau_recovery"

func (p *passkeyServer) recoveryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /account/recovery-codes/options", p.verification.protect(p.beginRecoveryCodeRotation))
	mux.HandleFunc("POST /account/recovery-codes/finish", p.verification.protect(p.finishRecoveryCodeRotation))
	mux.HandleFunc("POST /recovery/email", p.verification.protect(p.startRecovery))
	mux.HandleFunc("POST /recovery/email/verify", p.verification.protect(p.verifyRecoveryEmail))
	mux.HandleFunc("POST /recovery/passkey/options", p.verification.protect(p.beginRecoveredPasskey))
	mux.HandleFunc("POST /recovery/passkey/finish", p.verification.protect(p.finishRecovery))
}

// Offline-code guesses, including unknown accounts, consume a separate source
// budget. Successful sends additionally share registration's source send budget.
func (p *passkeyServer) reserveRecoveryAttempt(r *http.Request) (time.Duration, error) {
	s := p.verification
	hash, err := s.sourceHash(r)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := s.now()
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO recovery_attempt_limits (source_hash,window_start,attempt_count) VALUES (?,?,0) ON DUPLICATE KEY UPDATE source_hash=source_hash`, hash, now); err != nil {
		return 0, err
	}
	var start time.Time
	var count int
	if err = tx.QueryRowContext(r.Context(), `SELECT window_start,attempt_count FROM recovery_attempt_limits WHERE source_hash=? FOR UPDATE`, hash).Scan(&start, &count); err != nil {
		return 0, err
	}
	if !now.Before(start.Add(s.config.SendWindow)) {
		start = now
		count = 0
	}
	if count >= s.config.MaxSends {
		return start.Add(s.config.SendWindow).Sub(now), nil
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE recovery_attempt_limits SET window_start=?,attempt_count=? WHERE source_hash=?`, start, count+1, hash); err != nil {
		return 0, err
	}
	return 0, tx.Commit()
}
func retryRecovery(w http.ResponseWriter, delay time.Duration, message string) {
	w.Header().Set("Retry-After", fmt.Sprint(max(1, int(delay.Seconds())+1)))
	problem(w, 429, message)
}
func (p *passkeyServer) startRecovery(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email        string `json:"email"`
		RecoveryCode string `json:"recoveryCode"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	remaining, err := p.reserveRecoveryAttempt(r)
	if err != nil {
		problem(w, 503, "Recovery is temporarily unavailable")
		return
	}
	if remaining > 0 {
		retryRecovery(w, remaining, "Too many recovery attempts. Try again later")
		return
	}
	email, emailErr := normalizedEmail(input.Email)
	hash, codeErr := recoveryCodeHash(input.RecoveryCode)
	if emailErr != nil || codeErr != nil {
		problem(w, 400, "Email or recovery code is incorrect")
		return
	}
	s := p.verification
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Recovery is temporarily unavailable")
		return
	}
	defer tx.Rollback()
	now := s.now()
	remaining, err = s.reserveSend(r, tx, now)
	if err != nil {
		problem(w, 503, "Recovery is temporarily unavailable")
		return
	}
	if remaining > 0 {
		retryRecovery(w, remaining, "Too many email requests. Try again later")
		return
	}
	var id []byte
	err = tx.QueryRowContext(r.Context(), `SELECT id FROM accounts WHERE email=? FOR UPDATE`, email).Scan(&id)
	if err == sql.ErrNoRows {
		problem(w, 400, "Email or recovery code is incorrect")
		return
	}
	if err != nil {
		problem(w, 503, "Recovery is temporarily unavailable")
		return
	}
	var exists int
	err = tx.QueryRowContext(r.Context(), `SELECT 1 FROM recovery_codes WHERE account_id=? AND code_hash=? FOR UPDATE`, id, hash).Scan(&exists)
	if err == sql.ErrNoRows {
		problem(w, 400, "Email or recovery code is incorrect")
		return
	}
	if err != nil {
		problem(w, 503, "Recovery is temporarily unavailable")
		return
	}
	epoch := time.Unix(0, 0).UTC()
	_, err = tx.ExecContext(r.Context(), `INSERT INTO recovery_delivery_limits (account_id,last_sent_at,window_start,sent_count) VALUES (?,?,?,0) ON DUPLICATE KEY UPDATE account_id=account_id`, id, epoch, now)
	if err != nil {
		problem(w, 503, "Recovery is temporarily unavailable")
		return
	}
	var last, start time.Time
	var count int
	if err = tx.QueryRowContext(r.Context(), `SELECT last_sent_at,window_start,sent_count FROM recovery_delivery_limits WHERE account_id=? FOR UPDATE`, id).Scan(&last, &start, &count); err != nil {
		problem(w, 503, "Recovery is temporarily unavailable")
		return
	}
	if delay := last.Add(s.config.ResendInterval).Sub(now); delay > 0 {
		retryRecovery(w, delay, "Wait before requesting another recovery email")
		return
	}
	if !now.Before(start.Add(s.config.SendWindow)) {
		start = now
		count = 0
	}
	if count >= s.config.MaxSends {
		retryRecovery(w, start.Add(s.config.SendWindow).Sub(now), "Too many recovery emails for this account")
		return
	}
	token, err := newToken()
	if err != nil {
		problem(w, 500, "Could not start recovery")
		return
	}
	code, err := newCode()
	if err != nil {
		problem(w, 500, "Could not start recovery")
		return
	}
	// A resend in this browser replaces its pending flow. Other browsers' verified
	// flows remain valid until success rotates all codes, or their original expiry.
	if old := cookieToken(r, recoveryCookie); old != "" {
		if _, err = tx.ExecContext(r.Context(), `DELETE FROM recovery_requests WHERE token_hash=? AND account_id=?`, tokenHash(old), id); err != nil {
			problem(w, 503, "Recovery is temporarily unavailable")
			return
		}
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO recovery_requests (token_hash,account_id,recovery_hash,email_mac,expires_at) VALUES (?,?,?,?,?)`, tokenHash(token), id, hash, s.codeMAC("recovery:"+token, code), now.Add(s.config.CodeTTL))
	if err != nil {
		problem(w, 503, "Recovery is temporarily unavailable")
		return
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE recovery_delivery_limits SET last_sent_at=?,window_start=?,sent_count=? WHERE account_id=?`, now, start, count+1, id)
	if err != nil {
		problem(w, 503, "Recovery is temporarily unavailable")
		return
	}
	if sender, ok := s.sender.(interface {
		SendRecovery(context.Context, string, string) error
	}); ok {
		err = sender.SendRecovery(r.Context(), email, code)
	} else {
		err = s.sender.Send(r.Context(), email, code)
	}
	if err != nil {
		problem(w, 502, "Could not send recovery email. Try again")
		return
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Could not start recovery. Try again later")
		return
	}
	p.writeCookie(w, recoveryCookie, token, int((s.config.CodeTTL + s.config.RegistrationTTL).Seconds()))
	writeJSON(w, 202, map[string]any{"email": email, "retryAfterSeconds": int(s.config.ResendInterval.Seconds())})
}

type recoveryState struct {
	user            registrationUser
	token           string
	expires         time.Time
	verified        sql.NullTime
	mac, data       []byte
	challengeExpiry sql.NullTime
	attempts        int
}

// Resolve first, then lock account before child state. This serializes recovery,
// normal login and backup enrollment so lost credentials cannot race revocation.
func (p *passkeyServer) recoveryTransaction(w http.ResponseWriter, r *http.Request, action func(*sql.Tx, recoveryState) (int, any, error)) {
	token := cookieToken(r, recoveryCookie)
	if token == "" {
		problem(w, 401, "Start account recovery first")
		return
	}
	tx, err := p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Recovery is temporarily unavailable")
		return
	}
	defer tx.Rollback()
	state := recoveryState{token: token}
	err = tx.QueryRowContext(r.Context(), `SELECT account_id FROM recovery_requests WHERE token_hash=?`, tokenHash(token)).Scan(&state.user.ID)
	if err == nil {
		err = tx.QueryRowContext(r.Context(), `SELECT email FROM accounts WHERE id=? FOR UPDATE`, state.user.ID).Scan(&state.user.Email)
	}
	if err == nil {
		err = tx.QueryRowContext(r.Context(), `SELECT email_mac,expires_at,failed_attempts,verified_until,session_data,challenge_expires_at FROM recovery_requests WHERE token_hash=? AND account_id=? FOR UPDATE`, tokenHash(token), state.user.ID).Scan(&state.mac, &state.expires, &state.attempts, &state.verified, &state.data, &state.challengeExpiry)
	}
	if err == sql.ErrNoRows {
		problem(w, 401, "Recovery expired or was already completed. Start again")
		return
	}
	if err != nil {
		problem(w, 503, "Recovery is temporarily unavailable")
		return
	}
	if !p.verification.now().Before(state.expires) {
		problem(w, 410, "Recovery expired. Start again")
		return
	}
	status, result, err := action(tx, state)
	if err != nil {
		problem(w, 503, "Recovery is temporarily unavailable")
		return
	}
	if !p.verification.now().Before(state.expires) {
		problem(w, 410, "Recovery expired. Start again")
		return
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Could not complete recovery. Check before retrying")
		return
	}
	if success, ok := result.(recoverySuccess); ok {
		success.SignedIn = true
		p.writeCookie(w, sessionCookie, success.token, int(p.sessionTTL.Seconds()))
		p.writeCookie(w, recoveryCookie, "", -1)
		p.writeCookie(w, loginCookie, "", -1)
		result = success
	}
	writeJSON(w, status, result)
}
func (p *passkeyServer) verifyRecoveryEmail(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Code string `json:"code"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	p.recoveryTransaction(w, r, func(tx *sql.Tx, s recoveryState) (int, any, error) {
		if s.verified.Valid || len(s.mac) == 0 {
			return backupError(409, "This email code has already been used")
		}
		if s.attempts >= p.verification.config.MaxAttempts {
			return backupError(429, "Too many attempts. Request a new email code")
		}
		if len(input.Code) != 6 || !hmac.Equal(s.mac, p.verification.codeMAC("recovery:"+s.token, input.Code)) {
			_, err := tx.ExecContext(r.Context(), `UPDATE recovery_requests SET failed_attempts=failed_attempts+1 WHERE token_hash=?`, tokenHash(s.token))
			if err != nil {
				return 0, nil, err
			}
			if s.attempts+1 >= p.verification.config.MaxAttempts {
				return backupError(429, "Too many attempts. Request a new email code")
			}
			return backupError(400, "The recovery email code is incorrect")
		}
		until := p.verification.now().Add(p.verification.config.RegistrationTTL)
		_, err := tx.ExecContext(r.Context(), `UPDATE recovery_requests SET email_mac=NULL,verified_until=?,expires_at=? WHERE token_hash=?`, until, until, tokenHash(s.token))
		return 200, map[string]any{"verified": true, "expiresAt": until}, err
	})
}
func (p *passkeyServer) beginRecoveredPasskey(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if !decodeBody(w, r, &input) {
		return
	}
	p.recoveryTransaction(w, r, func(tx *sql.Tx, s recoveryState) (int, any, error) {
		if !s.verified.Valid {
			return backupError(401, "Verify the recovery email code first")
		}
		// Exclude the lost credentials on the client as well as checking uniqueness
		// on the server. The recovered account keeps its original WebAuthn user ID.
		rows, err := tx.QueryContext(r.Context(), `SELECT credential FROM passkey_credentials WHERE account_id=?`, s.user.ID)
		if err != nil {
			return 0, nil, err
		}
		var excluded []protocol.CredentialDescriptor
		for rows.Next() {
			var raw []byte
			var c webauthn.Credential
			if err = rows.Scan(&raw); err != nil {
				break
			}
			if err = json.Unmarshal(raw, &c); err != nil {
				break
			}
			excluded = append(excluded, c.Descriptor())
		}
		rowErr := rows.Err()
		rows.Close()
		if err != nil {
			return 0, nil, err
		}
		if rowErr != nil {
			return 0, nil, rowErr
		}
		options, state, err := p.wa.BeginRegistration(s.user, webauthn.WithExclusions(excluded))
		if err != nil {
			return 0, nil, err
		}
		expires := minTime(p.verification.now().Add(p.challengeTTL), s.expires)
		state.Expires = expires
		options.Response.Timeout = int(expires.Sub(p.verification.now()).Milliseconds())
		data, err := json.Marshal(state)
		if err != nil {
			return 0, nil, err
		}
		_, err = tx.ExecContext(r.Context(), `UPDATE recovery_requests SET session_data=?,challenge_expires_at=? WHERE token_hash=?`, data, expires, tokenHash(s.token))
		return 200, options.Response, err
	})
}
func (p *passkeyServer) finishRecovery(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	p.recoveryTransaction(w, r, func(tx *sql.Tx, s recoveryState) (int, any, error) {
		if !s.verified.Valid {
			return backupError(401, "Verify the recovery email code first")
		}
		if len(s.data) == 0 || !s.challengeExpiry.Valid {
			return backupError(409, "Start a new recovery Passkey registration")
		}
		if _, err := tx.ExecContext(r.Context(), `UPDATE recovery_requests SET session_data=NULL,challenge_expires_at=NULL WHERE token_hash=?`, tokenHash(s.token)); err != nil {
			return 0, nil, err
		}
		if !p.verification.now().Before(s.challengeExpiry.Time) {
			return backupError(410, "Passkey challenge expired. Try registration again")
		}
		var state webauthn.SessionData
		if err := json.Unmarshal(s.data, &state); err != nil {
			return 0, nil, err
		}
		credential, err := p.wa.FinishRegistration(s.user, state, r)
		if err != nil {
			return backupError(400, "Recovery Passkey registration failed. Try again")
		}
		hash := sha256.Sum256(credential.ID)
		var exists int
		if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM passkey_credentials WHERE id_hash=?`, hash[:]).Scan(&exists); err != nil {
			return 0, nil, err
		}
		if exists > 0 {
			return backupError(409, "Choose a new Passkey for recovery")
		}
		raw, err := json.Marshal(credential)
		if err != nil {
			return 0, nil, err
		}
		for _, table := range []string{"passkey_credentials", "account_sessions"} {
			if _, err = tx.ExecContext(r.Context(), `DELETE FROM `+table+` WHERE account_id=?`, s.user.ID); err != nil {
				return 0, nil, err
			}
		}
		now := p.verification.now()
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO passkey_credentials (id_hash,account_id,credential,created_at) VALUES (?,?,?,?)`, hash[:], s.user.ID, raw, now); err != nil {
			return 0, nil, err
		}
		token, err := newToken()
		if err != nil {
			return 0, nil, err
		}
		expires := now.Add(p.sessionTTL)
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO account_sessions (token_hash,account_id,expires_at,absolute_expires_at) VALUES (?,?,?,?)`, tokenHash(token), s.user.ID, expires, now.Add(p.sessionAbsoluteTTL)); err != nil {
			return 0, nil, err
		}
		codes, err := replaceRecoveryCodes(r.Context(), tx, s.user.ID)
		if err != nil {
			return 0, nil, err
		}
		// Cookie mutation must wait for the transaction commit, not happen here.
		return 200, recoverySuccess{Email: s.user.Email, ExpiresAt: expires, RenewAfterSeconds: p.renewAfterSeconds(), RecoveryCodes: codes, token: token}, nil
	})
}

type recoverySuccess struct {
	SignedIn          bool      `json:"signedIn"`
	Email             string    `json:"email"`
	ExpiresAt         time.Time `json:"expiresAt"`
	RenewAfterSeconds int       `json:"renewAfterSeconds"`
	RecoveryCodes     []string  `json:"recoveryCodes"`
	token             string
}
