package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// Backup ceremonies belong to the exact authenticated session, not just an
// account. Logging out or rotating that session removes its authorization.
type backupSession struct {
	user    loginUser
	hash    []byte
	expires time.Time
}

// Lock the account before its session/credentials, matching login's account
// lock. Re-read the session after locking so logout/expiry cannot authorize work.
func (p *passkeyServer) lockBackupSession(ctx context.Context, tx *sql.Tx, token string) (backupSession, error) {
	s := backupSession{hash: tokenHash(token)}
	if token == "" {
		return s, sql.ErrNoRows
	}
	if err := tx.QueryRowContext(ctx, `SELECT account_id FROM account_sessions WHERE token_hash=?`, s.hash).Scan(&s.user.ID); err != nil {
		return s, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT email FROM accounts WHERE id=? FOR UPDATE`, s.user.ID).Scan(&s.user.Email); err != nil {
		return s, err
	}
	var idle, absolute time.Time
	if err := tx.QueryRowContext(ctx, `SELECT expires_at,absolute_expires_at FROM account_sessions WHERE token_hash=? AND account_id=? FOR UPDATE`, s.hash, s.user.ID).Scan(&idle, &absolute); err != nil {
		return s, err
	}
	s.expires = minTime(idle, absolute)
	if !p.verification.now().Before(s.expires) {
		return s, sql.ErrNoRows
	}
	rows, err := tx.QueryContext(ctx, `SELECT credential FROM passkey_credentials WHERE account_id=? ORDER BY id_hash FOR UPDATE`, s.user.ID)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return s, err
		}
		var c webauthn.Credential
		if err = json.Unmarshal(raw, &c); err != nil {
			return s, err
		}
		s.user.Credentials = append(s.user.Credentials, c)
	}
	return s, rows.Err()
}

type backupAction func(*sql.Tx, backupSession) (int, any, error)

func backupError(status int, message string) (int, any, error) {
	return status, map[string]string{"error": message}, nil
}

// Validation failures commit challenge consumption; storage failures roll back.
func (p *passkeyServer) backupTransaction(w http.ResponseWriter, r *http.Request, action backupAction) {
	tx, err := p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Passkey enrollment is temporarily unavailable")
		return
	}
	defer tx.Rollback()
	s, err := p.lockBackupSession(r.Context(), tx, cookieToken(r, sessionCookie))
	if err == sql.ErrNoRows {
		problem(w, 401, "Sign in again before adding a Passkey")
		return
	}
	if err != nil {
		problem(w, 503, "Passkey enrollment is temporarily unavailable")
		return
	}
	status, result, err := action(tx, s)
	if err != nil {
		problem(w, 503, "Passkey enrollment is temporarily unavailable")
		return
	}
	if !p.verification.now().Before(s.expires) {
		problem(w, 401, "Sign in again before adding a Passkey")
		return
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Could not complete enrollment. Check before retrying")
		return
	}
	writeJSON(w, status, result)
}
func (p *passkeyServer) saveBackupState(ctx context.Context, tx *sql.Tx, s backupSession, stage string, state *webauthn.SessionData, expires time.Time) error {
	state.Expires = expires
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO backup_passkey_ceremonies (session_hash,stage,session_data,expires_at) VALUES (?,?,?,?) ON DUPLICATE KEY UPDATE stage=VALUES(stage),session_data=VALUES(session_data),expires_at=VALUES(expires_at)`, s.hash, stage, raw, expires)
	return err
}
func (p *passkeyServer) beginBackupVerification(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if !decodeBody(w, r, &input) {
		return
	}
	p.backupTransaction(w, r, func(tx *sql.Tx, s backupSession) (int, any, error) {
		options, state, err := p.wa.BeginLogin(s.user, webauthn.WithUserVerification(protocol.VerificationRequired))
		if err != nil {
			return 0, nil, err
		}
		expires := minTime(p.verification.now().Add(p.challengeTTL), s.expires)
		options.Response.Timeout = int(expires.Sub(p.verification.now()).Milliseconds())
		err = p.saveBackupState(r.Context(), tx, s, "verification", state, expires)
		return 200, options.Response, err
	})
}

// The stage transition is single-use. A replay of verification must not consume
// the registration stage produced by the original successful request.
func (p *passkeyServer) backupFinish(w http.ResponseWriter, r *http.Request, stage string, action func(*sql.Tx, backupSession, webauthn.SessionData) (int, any, error)) {
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	p.backupTransaction(w, r, func(tx *sql.Tx, s backupSession) (int, any, error) {
		var raw []byte
		var savedStage string
		var expires time.Time
		err := tx.QueryRowContext(r.Context(), `SELECT stage,session_data,expires_at FROM backup_passkey_ceremonies WHERE session_hash=? FOR UPDATE`, s.hash).Scan(&savedStage, &raw, &expires)
		if err == sql.ErrNoRows {
			return backupError(409, "Start a new Add a Passkey attempt")
		}
		if err != nil {
			return 0, nil, err
		}
		if savedStage != stage {
			return backupError(409, "This enrollment step is no longer available. Start again if needed")
		}
		if _, err = tx.ExecContext(r.Context(), `DELETE FROM backup_passkey_ceremonies WHERE session_hash=?`, s.hash); err != nil {
			return 0, nil, err
		}
		if !p.verification.now().Before(expires) {
			return backupError(410, "Passkey enrollment expired. Start again")
		}
		var state webauthn.SessionData
		if err = json.Unmarshal(raw, &state); err != nil {
			return 0, nil, err
		}
		return action(tx, s, state)
	})
}
func (p *passkeyServer) verifyBackupPasskey(w http.ResponseWriter, r *http.Request) {
	p.backupFinish(w, r, "verification", func(tx *sql.Tx, s backupSession, state webauthn.SessionData) (int, any, error) {
		credential, err := p.wa.FinishLogin(s.user, state, r)
		if err != nil {
			return backupError(401, "Existing Passkey verification failed. Start again")
		}
		if credential.Authenticator.CloneWarning {
			return backupError(401, "Existing Passkey verification failed. Try another Passkey")
		}
		raw, err := json.Marshal(credential)
		if err != nil {
			return 0, nil, err
		}
		hash := sha256.Sum256(credential.ID)
		if _, err = tx.ExecContext(r.Context(), `UPDATE passkey_credentials SET credential=? WHERE id_hash=? AND account_id=?`, raw, hash[:], s.user.ID); err != nil {
			return 0, nil, err
		}
		excluded := make([]protocol.CredentialDescriptor, 0, len(s.user.Credentials))
		for _, c := range s.user.Credentials {
			excluded = append(excluded, c.Descriptor())
		}
		options, next, err := p.wa.BeginRegistration(s.user, webauthn.WithExclusions(excluded))
		if err != nil {
			return 0, nil, err
		}
		expires := minTime(p.verification.now().Add(p.challengeTTL), s.expires)
		options.Response.Timeout = int(expires.Sub(p.verification.now()).Milliseconds())
		err = p.saveBackupState(r.Context(), tx, s, "registration", next, expires)
		return 200, options.Response, err
	})
}
func (p *passkeyServer) finishBackupPasskey(w http.ResponseWriter, r *http.Request) {
	p.backupFinish(w, r, "registration", func(tx *sql.Tx, s backupSession, state webauthn.SessionData) (int, any, error) {
		credential, err := p.wa.FinishRegistration(s.user, state, r)
		if err != nil {
			return backupError(400, "New Passkey registration failed. Start again")
		}
		raw, err := json.Marshal(credential)
		if err != nil {
			return 0, nil, err
		}
		hash := sha256.Sum256(credential.ID)
		_, err = tx.ExecContext(r.Context(), `INSERT INTO passkey_credentials (id_hash,account_id,credential,created_at) VALUES (?,?,?,?)`, hash[:], s.user.ID, raw, p.verification.now())
		if duplicateKey(err) {
			return backupError(409, "This Passkey is already registered. Start again with another Passkey")
		}
		if err != nil {
			return 0, nil, err
		}
		return 201, map[string]bool{"added": true}, nil
	})
}
