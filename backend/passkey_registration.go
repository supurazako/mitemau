package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

const sessionCookie = "mitemau_session"

type registrationUser struct {
	ID    []byte
	Email string
}

func (u registrationUser) WebAuthnID() []byte                         { return u.ID }
func (u registrationUser) WebAuthnName() string                       { return u.Email }
func (u registrationUser) WebAuthnDisplayName() string                { return u.Email }
func (u registrationUser) WebAuthnCredentials() []webauthn.Credential { return nil }

type passkeyServer struct {
	verification             *verificationServer
	wa                       *webauthn.WebAuthn
	challengeTTL, sessionTTL time.Duration
	sessionAbsoluteTTL       time.Duration
}

func newPasskeyServer(v *verificationServer) (*passkeyServer, error) {
	challengeTTL, err := positiveDuration("PASSKEY_CHALLENGE_TTL", "5m")
	if err != nil {
		return nil, err
	}
	sessionTTL, err := positiveDuration("SESSION_TTL", "24h")
	if err != nil {
		return nil, err
	}
	absoluteTTL, err := positiveDuration("SESSION_ABSOLUTE_TTL", "168h")
	if err != nil {
		return nil, err
	}
	if absoluteTTL < sessionTTL {
		return nil, errors.New("SESSION_ABSOLUTE_TTL must be at least SESSION_TTL")
	}
	rpID := envDefault("WEBAUTHN_RP_ID", "localhost")
	origin, err := url.Parse(v.config.Origin)
	if err != nil {
		return nil, err
	}
	if origin.Hostname() != rpID || (origin.Scheme != "https" && !(origin.Scheme == "http" && rpID == "localhost")) {
		return nil, errors.New("APP_ORIGIN must use HTTPS (or http://localhost) and match WEBAUTHN_RP_ID")
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPID: rpID, RPDisplayName: "Mitemau", RPOrigins: []string{v.config.Origin},
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationRequired,
		},
		Timeouts: webauthn.TimeoutsConfig{Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: challengeTTL}, Login: webauthn.TimeoutConfig{Enforce: true, Timeout: challengeTTL}},
	})
	if err != nil {
		return nil, err
	}
	return &passkeyServer{verification: v, wa: wa, challengeTTL: challengeTTL, sessionTTL: sessionTTL, sessionAbsoluteTTL: absoluteTTL}, nil
}
func (p *passkeyServer) routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /registration/passkey/options", p.verification.protect(p.begin))
	mux.HandleFunc("POST /registration/passkey/finish", p.verification.protect(p.finish))
	mux.HandleFunc("GET /session", p.session)
	mux.HandleFunc("POST /session/renew", p.verification.protect(p.renewSession))
	mux.HandleFunc("POST /login/passkey/options", p.verification.protect(p.beginLogin))
	mux.HandleFunc("POST /login/passkey/finish", p.verification.protect(p.finishLogin))
	mux.HandleFunc("POST /account/passkeys/verify/options", p.verification.protect(p.beginBackupVerification))
	mux.HandleFunc("POST /account/passkeys/verify/finish", p.verification.protect(p.verifyBackupPasskey))
	mux.HandleFunc("POST /account/passkeys/finish", p.verification.protect(p.finishBackupPasskey))
	mux.HandleFunc("POST /logout", p.verification.protect(p.signOut))
}
func registrationGrant(ctx context.Context, tx *sql.Tx, token string, now time.Time) (registrationUser, time.Time, error) {
	var user registrationUser
	var expiry time.Time
	if token == "" {
		return user, expiry, sql.ErrNoRows
	}
	err := tx.QueryRowContext(ctx, `SELECT email,expires_at FROM verified_registrations WHERE token_hash=? AND expires_at>? FOR UPDATE`, tokenHash(token), now).Scan(&user.Email, &expiry)
	return user, expiry, err
}
func duplicateKey(err error) bool {
	var dbErr *mysql.MySQLError
	return errors.As(err, &dbErr) && dbErr.Number == 1062
}
func (p *passkeyServer) begin(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if !decodeBody(w, r, &input) {
		return
	}
	token := browserToken(r)
	tx, err := p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Registration is temporarily unavailable")
		return
	}
	defer tx.Rollback()
	now := p.verification.now()
	user, grantExpiry, err := registrationGrant(r.Context(), tx, token, now)
	if err == sql.ErrNoRows {
		problem(w, 401, "Verify your email address before registering a Passkey")
		return
	}
	if err != nil {
		problem(w, 503, "Registration is temporarily unavailable")
		return
	}
	var exists int
	if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM accounts WHERE email=?`, user.Email).Scan(&exists); err != nil {
		problem(w, 503, "Registration is temporarily unavailable")
		return
	}
	if exists > 0 {
		problem(w, 409, "This email address is already registered. Please sign in")
		return
	}
	user.ID = make([]byte, 32)
	if _, err = rand.Read(user.ID); err != nil {
		problem(w, 500, "Could not start Passkey registration")
		return
	}
	options, state, err := p.wa.BeginRegistration(user)
	if err != nil {
		problem(w, 500, "Could not start Passkey registration")
		return
	}
	expires := minTime(now.Add(p.challengeTTL), grantExpiry)
	state.Expires = expires
	options.Response.Timeout = int(expires.Sub(now).Milliseconds())
	data, err := json.Marshal(state)
	if err != nil {
		problem(w, 500, "Could not start Passkey registration")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO registration_ceremonies (registration_hash,user_id,session_data,expires_at) VALUES (?,?,?,?) ON DUPLICATE KEY UPDATE user_id=VALUES(user_id),session_data=VALUES(session_data),expires_at=VALUES(expires_at)`, tokenHash(token), user.ID, data, expires)
	if err != nil {
		problem(w, 503, "Registration is temporarily unavailable")
		return
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Registration is temporarily unavailable")
		return
	}
	// The browser parser expects the inner PublicKeyCredentialCreationOptionsJSON.
	writeJSON(w, 200, options.Response)
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (p *passkeyServer) finish(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	token := browserToken(r)
	tx, err := p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Registration is temporarily unavailable")
		return
	}
	defer tx.Rollback()
	now := p.verification.now()
	user, _, err := registrationGrant(r.Context(), tx, token, now)
	if err == sql.ErrNoRows {
		problem(w, 401, "Email verification expired or was already used. Verify your email again")
		return
	}
	if err != nil {
		problem(w, 503, "Registration is temporarily unavailable")
		return
	}
	var data []byte
	var expires time.Time
	err = tx.QueryRowContext(r.Context(), `SELECT user_id,session_data,expires_at FROM registration_ceremonies WHERE registration_hash=? FOR UPDATE`, tokenHash(token)).Scan(&user.ID, &data, &expires)
	if err == sql.ErrNoRows {
		problem(w, 409, "Start a new Passkey registration attempt")
		return
	}
	if err != nil {
		problem(w, 503, "Registration is temporarily unavailable")
		return
	}
	// Consume on every completed server-side attempt; retries begin a fresh challenge.
	if _, err = tx.ExecContext(r.Context(), `DELETE FROM registration_ceremonies WHERE registration_hash=?`, tokenHash(token)); err != nil {
		problem(w, 503, "Registration is temporarily unavailable")
		return
	}
	reject := func(status int, message string) {
		if err := tx.Commit(); err != nil {
			problem(w, 503, "Registration is temporarily unavailable")
			return
		}
		problem(w, status, message)
	}
	if !now.Before(expires) {
		reject(410, "Passkey registration expired. Please try again")
		return
	}
	var state webauthn.SessionData
	if err = json.Unmarshal(data, &state); err != nil {
		problem(w, 503, "Registration is temporarily unavailable")
		return
	}
	credential, err := p.wa.FinishRegistration(user, state, r)
	if err != nil {
		reject(400, "Passkey registration could not be verified. Please try again")
		return
	}
	credentialJSON, err := json.Marshal(credential)
	if err != nil {
		problem(w, 500, "Could not save the Passkey")
		return
	}
	credentialHash := sha256.Sum256(credential.ID)
	newSession, err := newToken()
	if err != nil {
		problem(w, 500, "Could not create a session")
		return
	}
	// Account uniqueness is enforced by the DB even across separately verified browsers.
	_, err = tx.ExecContext(r.Context(), `INSERT INTO accounts (id,email,created_at) VALUES (?,?,?)`, user.ID, user.Email, now)
	if duplicateKey(err) {
		reject(409, "This email address is already registered. Please sign in")
		return
	}
	if err != nil {
		problem(w, 503, "Registration is temporarily unavailable")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO passkey_credentials (id_hash,account_id,credential,created_at) VALUES (?,?,?,?)`, credentialHash[:], user.ID, credentialJSON, now)
	if err != nil {
		problem(w, 503, "Could not save the Passkey")
		return
	}
	sessionExpires := now.Add(p.sessionTTL)
	_, err = tx.ExecContext(r.Context(), `INSERT INTO account_sessions (token_hash,account_id,expires_at,absolute_expires_at) VALUES (?,?,?,?)`, tokenHash(newSession), user.ID, sessionExpires, now.Add(p.sessionAbsoluteTTL))
	if err != nil {
		problem(w, 503, "Could not create a session")
		return
	}
	// Consume this browser's email grant in the same transaction as account creation.
	_, err = tx.ExecContext(r.Context(), `DELETE FROM verified_registrations WHERE token_hash=?`, tokenHash(token))
	if err != nil {
		problem(w, 503, "Registration is temporarily unavailable")
		return
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Could not complete registration. Check your account status before retrying")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: newSession, Path: "/", MaxAge: int(p.sessionTTL.Seconds()), HttpOnly: true, Secure: p.verification.config.SecureCookie, SameSite: http.SameSiteStrictMode})
	http.SetCookie(w, &http.Cookie{Name: registrationCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: p.verification.config.SecureCookie, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 201, map[string]any{"email": user.Email, "signedIn": true, "expiresAt": sessionExpires, "renewAfterSeconds": p.renewAfterSeconds()})
}
func (p *passkeyServer) session(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || len(cookie.Value) != 64 {
		writeJSON(w, 200, map[string]bool{"signedIn": false})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var email string
	var expires, absolute time.Time
	err = p.verification.db.QueryRowContext(ctx, `SELECT a.email,s.expires_at,s.absolute_expires_at FROM account_sessions s JOIN accounts a ON a.id=s.account_id WHERE s.token_hash=? AND s.expires_at>? AND s.absolute_expires_at>?`, tokenHash(cookie.Value), p.verification.now(), p.verification.now()).Scan(&email, &expires, &absolute)
	if err == sql.ErrNoRows {
		writeJSON(w, 200, map[string]bool{"signedIn": false})
		return
	}
	if err != nil {
		problem(w, 503, "Session is temporarily unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"email": email, "signedIn": true, "expiresAt": minTime(expires, absolute), "renewAfterSeconds": p.renewAfterSeconds()})
}

// Comparing normalized email addresses follows the existing ASCII policy.
func (p *passkeyServer) registeredEmail(ctx context.Context, email string) (bool, error) {
	var count int
	err := p.verification.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE email=?`, strings.TrimSpace(email)).Scan(&count)
	return count > 0, err
}
