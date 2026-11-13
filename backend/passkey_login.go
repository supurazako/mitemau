package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

const loginCookie = "mitemau_login"

type loginUser struct {
	registrationUser
	Credentials []webauthn.Credential
}

func (u loginUser) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }

func cookieToken(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil || len(c.Value) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(c.Value); err != nil {
		return ""
	}
	return c.Value
}
func (p *passkeyServer) writeCookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: p.verification.config.SecureCookie, SameSite: http.SameSiteStrictMode})
}
func (p *passkeyServer) beginLogin(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if !decodeBody(w, r, &input) {
		return
	}
	token := cookieToken(r, loginCookie)
	if token == "" {
		var err error
		token, err = newToken()
		if err != nil {
			problem(w, 500, "Could not start sign-in")
			return
		}
	}
	options, state, err := p.wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		problem(w, 500, "Could not start sign-in")
		return
	}
	expires := p.verification.now().Add(p.challengeTTL)
	state.Expires = expires
	data, err := json.Marshal(state)
	if err != nil {
		problem(w, 500, "Could not start sign-in")
		return
	}
	_, err = p.verification.db.ExecContext(r.Context(), `INSERT INTO login_ceremonies (token_hash,session_data,expires_at) VALUES (?,?,?) ON DUPLICATE KEY UPDATE session_data=VALUES(session_data),expires_at=VALUES(expires_at)`, tokenHash(token), data, expires)
	if err != nil {
		problem(w, 503, "Sign-in is temporarily unavailable")
		return
	}
	p.writeCookie(w, loginCookie, token, int(p.challengeTTL.Seconds()))
	writeJSON(w, 200, options.Response)
}
func (p *passkeyServer) finishLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	token := cookieToken(r, loginCookie)
	if token == "" {
		problem(w, 401, "Start a new sign-in attempt")
		return
	}
	tx, err := p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Sign-in is temporarily unavailable")
		return
	}
	defer tx.Rollback()
	var data []byte
	var expires time.Time
	err = tx.QueryRowContext(r.Context(), `SELECT session_data,expires_at FROM login_ceremonies WHERE token_hash=? FOR UPDATE`, tokenHash(token)).Scan(&data, &expires)
	if err == sql.ErrNoRows {
		problem(w, 401, "Start a new sign-in attempt")
		return
	}
	if err != nil {
		problem(w, 503, "Sign-in is temporarily unavailable")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `DELETE FROM login_ceremonies WHERE token_hash=?`, tokenHash(token)); err != nil {
		problem(w, 503, "Sign-in is temporarily unavailable")
		return
	}
	reject := func(status int, message string) {
		if err := tx.Commit(); err != nil {
			problem(w, 503, "Sign-in is temporarily unavailable")
			return
		}
		p.writeCookie(w, loginCookie, "", -1)
		problem(w, status, message)
	}
	now := p.verification.now()
	if !now.Before(expires) {
		reject(410, "Sign-in expired. Please try again")
		return
	}
	var state webauthn.SessionData
	if err = json.Unmarshal(data, &state); err != nil {
		problem(w, 503, "Sign-in is temporarily unavailable")
		return
	}
	var lookupErr error
	lookup := func(rawID, handle []byte) (webauthn.User, error) {
		hash := sha256.Sum256(rawID)
		var user loginUser
		// Resolve ownership, then lock the account before its credential. Future
		// credential/recovery operations must use the same lock order.
		e := tx.QueryRowContext(r.Context(), `SELECT account_id FROM passkey_credentials WHERE id_hash=?`, hash[:]).Scan(&user.ID)
		if e != nil {
			if e != sql.ErrNoRows {
				lookupErr = e
			}
			return nil, errors.New("unknown credential")
		}
		if !bytes.Equal(user.ID, handle) {
			return nil, errors.New("credential does not match user")
		}
		e = tx.QueryRowContext(r.Context(), `SELECT email FROM accounts WHERE id=? FOR UPDATE`, user.ID).Scan(&user.Email)
		if e != nil {
			if e != sql.ErrNoRows {
				lookupErr = e
			}
			return nil, errors.New("unknown account")
		}
		var saved []byte
		e = tx.QueryRowContext(r.Context(), `SELECT credential FROM passkey_credentials WHERE id_hash=? AND account_id=? FOR UPDATE`, hash[:], user.ID).Scan(&saved)
		if e != nil {
			if e != sql.ErrNoRows {
				lookupErr = e
			}
			return nil, errors.New("unknown credential")
		}
		var credential webauthn.Credential
		if e = json.Unmarshal(saved, &credential); e != nil {
			lookupErr = e
			return nil, e
		}
		if !bytes.Equal(credential.ID, rawID) {
			lookupErr = errors.New("credential record mismatch")
			return nil, lookupErr
		}
		user.Credentials = []webauthn.Credential{credential}
		return user, nil
	}
	user, credential, err := p.wa.FinishPasskeyLogin(lookup, state, r)
	if lookupErr != nil {
		problem(w, 503, "Sign-in is temporarily unavailable")
		return
	}
	if err != nil {
		reject(401, "Passkey sign-in could not be verified. Please try again")
		return
	}
	if credential.Authenticator.CloneWarning {
		reject(401, "Passkey sign-in could not be verified. Please try another Passkey")
		return
	}
	credentialData, err := json.Marshal(credential)
	if err != nil {
		problem(w, 500, "Could not update the Passkey")
		return
	}
	credentialHash := sha256.Sum256(credential.ID)
	if _, err = tx.ExecContext(r.Context(), `UPDATE passkey_credentials SET credential=? WHERE id_hash=? AND account_id=?`, credentialData, credentialHash[:], user.WebAuthnID()); err != nil {
		problem(w, 503, "Could not update the Passkey")
		return
	}
	newSession, err := newToken()
	if err != nil {
		problem(w, 500, "Could not create a session")
		return
	}
	sessionExpires := now.Add(p.sessionTTL)
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO account_sessions (token_hash,account_id,expires_at,absolute_expires_at) VALUES (?,?,?,?)`, tokenHash(newSession), user.WebAuthnID(), sessionExpires, now.Add(p.sessionAbsoluteTTL)); err != nil {
		problem(w, 503, "Could not create a session")
		return
	}
	// Rotate only this browser's prior session. Other devices stay signed in.
	if old := cookieToken(r, sessionCookie); old != "" {
		if _, err = tx.ExecContext(r.Context(), `DELETE FROM account_sessions WHERE token_hash=?`, tokenHash(old)); err != nil {
			problem(w, 503, "Could not replace the session")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Could not complete sign-in. Check your session before retrying")
		return
	}
	p.writeCookie(w, sessionCookie, newSession, int(p.sessionTTL.Seconds()))
	p.writeCookie(w, loginCookie, "", -1)
	writeJSON(w, 200, map[string]any{"signedIn": true, "email": user.WebAuthnName(), "expiresAt": sessionExpires, "renewAfterSeconds": p.renewAfterSeconds()})
}
func (p *passkeyServer) signOut(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if !decodeBody(w, r, &input) {
		return
	}
	tx, err := p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Sign-out is temporarily unavailable")
		return
	}
	defer tx.Rollback()
	// Match login's lock order when an unfinished login is also present.
	for _, item := range []struct{ name, table string }{{loginCookie, "login_ceremonies"}, {sessionCookie, "account_sessions"}} {
		if token := cookieToken(r, item.name); token != "" {
			if _, err = tx.ExecContext(r.Context(), "DELETE FROM "+item.table+" WHERE token_hash=?", tokenHash(token)); err != nil {
				problem(w, 503, "Could not sign out. Please try again")
				return
			}
		}
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Could not sign out. Please try again")
		return
	}
	p.writeCookie(w, sessionCookie, "", -1)
	p.writeCookie(w, loginCookie, "", -1)
	writeJSON(w, 200, map[string]bool{"signedIn": false})
}

// Session reads never extend expires_at. Authorization must always check the DB,
// even when the browser retains a cookie or a stale signed-in display.
