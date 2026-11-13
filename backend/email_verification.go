package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

const registrationCookie = "mitemau_registration"

type verificationConfig struct {
	Secret                                   []byte
	CodeTTL, RegistrationTTL, ResendInterval time.Duration
	MaxAttempts                              int
	MaxSends                                 int
	SendWindow, CleanupInterval              time.Duration
	TrustLoopbackProxy                       bool
	SecureCookie                             bool
	Origin                                   string
}

type mailSender interface {
	Send(context.Context, string, string) error
}

type verificationServer struct {
	db              *sql.DB
	sender          mailSender
	config          verificationConfig
	now             func() time.Time
	registeredEmail func(context.Context, string) (bool, error)
}

func newVerificationServer(db *sql.DB, sender mailSender, cfg verificationConfig) *verificationServer {
	return &verificationServer{db: db, sender: sender, config: cfg, now: func() time.Time { return time.Now().UTC() }}
}

func (s *verificationServer) routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /registration/email", s.protect(s.requestCode))
	mux.HandleFunc("POST /registration/email/verify", s.protect(s.verifyCode))
	mux.HandleFunc("GET /registration/email", s.status)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func problem(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// JSON-only mutation endpoints and an explicit frontend origin prevent browser
// cross-origin form submissions. Proxy headers are not trusted here.
func (s *verificationServer) protect(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && origin != s.config.Origin {
			problem(w, 403, "Request origin is not allowed")
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			problem(w, 403, "Cross-site requests are not allowed")
			return
		}
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" {
			problem(w, 415, "Expected application/json")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		next(w, r.WithContext(ctx))
	}
}
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		problem(w, 400, "Invalid request")
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		problem(w, 400, "Invalid request")
		return false
	}
	return true
}
func normalizedEmail(value string) (string, error) {
	value = strings.TrimSpace(value)
	addr, err := mail.ParseAddress(value)
	if err != nil || addr.Address != value || len(value) > 254 || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("invalid email")
	}
	// This initial policy supports ASCII addresses; preserve local-part case.
	for _, c := range value {
		if c > 127 {
			return "", errors.New("non-ASCII address")
		}
	}
	i := strings.LastIndexByte(value, '@')
	if i < 1 || len(value[:i]) > 64 {
		return "", errors.New("invalid email")
	}
	return value[:i+1] + strings.ToLower(value[i+1:]), nil
}
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func newCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n), nil
}
func (s *verificationServer) codeMAC(token, code string) []byte {
	h := hmac.New(sha256.New, s.config.Secret)
	h.Write([]byte(token))
	h.Write([]byte{0})
	h.Write([]byte(code))
	return h.Sum(nil)
}
func tokenHash(token string) []byte { h := sha256.Sum256([]byte(token)); return h[:] }
func browserToken(r *http.Request) string {
	c, err := r.Cookie(registrationCookie)
	if err != nil || len(c.Value) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(c.Value); err != nil {
		return ""
	}
	return c.Value
}
func (s *verificationServer) setCookie(w http.ResponseWriter, token string) {
	// Pending email verification and verified registration are distinct states.
	ttl := s.config.CodeTTL
	if s.config.RegistrationTTL > ttl {
		ttl = s.config.RegistrationTTL
	}
	http.SetCookie(w, &http.Cookie{Name: registrationCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.config.SecureCookie, SameSite: http.SameSiteStrictMode, MaxAge: int(ttl.Seconds())})
}

func (s *verificationServer) requestCode(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email string `json:"email"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	email, err := normalizedEmail(input.Email)
	if err != nil {
		problem(w, 400, "Enter a valid email address")
		return
	}
	if s.registeredEmail != nil {
		exists, err := s.registeredEmail(r.Context(), email)
		if err != nil {
			problem(w, 503, "Registration is temporarily unavailable")
			return
		}
		if exists {
			problem(w, 409, "This email address is already registered. Please sign in")
			return
		}
	}
	token, err := newToken()
	if err != nil {
		problem(w, 500, "Could not start verification")
		return
	}
	code, err := newCode()
	if err != nil {
		problem(w, 500, "Could not start verification")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Verification is temporarily unavailable")
		return
	}
	defer tx.Rollback()
	now := s.now()
	remaining, err := s.reserveSend(r, tx, now)
	if err != nil {
		problem(w, 503, "Verification is temporarily unavailable")
		return
	}
	if remaining > 0 {
		w.Header().Set("Retry-After", fmt.Sprint(int(remaining.Seconds())+1))
		problem(w, 429, "Too many email requests from this connection. Please try again later")
		return
	}
	// A unique email row serializes requests, including the first concurrent send.
	_, err = tx.ExecContext(r.Context(), `INSERT INTO email_verifications
        (email,token_hash,code_expires_at,last_sent_at) VALUES (?,?,?,?)
        ON DUPLICATE KEY UPDATE email=email`, email, tokenHash(token), time.Unix(0, 0).UTC(), time.Unix(0, 0).UTC())
	if err != nil {
		problem(w, 503, "Verification is temporarily unavailable")
		return
	}
	var last time.Time
	if err = tx.QueryRowContext(r.Context(), `SELECT last_sent_at FROM email_verifications WHERE email=? FOR UPDATE`, email).Scan(&last); err != nil {
		problem(w, 503, "Verification is temporarily unavailable")
		return
	}
	if remaining := last.Add(s.config.ResendInterval).Sub(now); remaining > 0 {
		w.Header().Set("Retry-After", fmt.Sprint(int(remaining.Seconds())+1))
		problem(w, 429, "Please wait before requesting another code")
		return
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE email_verifications SET token_hash=?,code_mac=?,code_expires_at=?,last_sent_at=?,failed_attempts=0,verified_until=NULL WHERE email=?`, tokenHash(token), s.codeMAC(token, code), now.Add(s.config.CodeTTL), now, email)
	if err != nil {
		problem(w, 503, "Verification is temporarily unavailable")
		return
	}
	// SMTP is bounded by a timeout. Failed sends roll back the new code and cooldown.
	if err = s.sender.Send(r.Context(), email, code); err != nil {
		problem(w, 502, "Could not send the email. Please try again")
		return
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Could not finish verification request. Please try again later")
		return
	}
	s.setCookie(w, token)
	writeJSON(w, 202, map[string]any{"email": email, "verified": false, "retryAfterSeconds": int(s.config.ResendInterval.Seconds())})
}
func (s *verificationServer) verifyCode(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Code string `json:"code"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	token := browserToken(r)
	if token == "" {
		problem(w, 400, "Request a verification code first")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Verification is temporarily unavailable")
		return
	}
	defer tx.Rollback()
	var email string
	var mac []byte
	var expires time.Time
	var attempts int
	var verified sql.NullTime
	err = tx.QueryRowContext(r.Context(), `SELECT email,code_mac,code_expires_at,failed_attempts,verified_until FROM email_verifications WHERE token_hash=? FOR UPDATE`, tokenHash(token)).Scan(&email, &mac, &expires, &attempts, &verified)
	if err == sql.ErrNoRows {
		problem(w, 400, "Request a new verification code")
		return
	}
	if err != nil {
		problem(w, 503, "Verification is temporarily unavailable")
		return
	}
	if verified.Valid || len(mac) == 0 {
		problem(w, 409, "This verification code has already been used")
		return
	}
	now := s.now()
	if !now.Before(expires) {
		problem(w, 410, "The verification code has expired. Request a new code")
		return
	}
	if attempts >= s.config.MaxAttempts {
		problem(w, 429, "Too many attempts. Request a new code")
		return
	}
	if len(input.Code) != 6 || !hmac.Equal(mac, s.codeMAC(token, input.Code)) {
		if _, err = tx.ExecContext(r.Context(), `UPDATE email_verifications SET failed_attempts=failed_attempts+1 WHERE email=?`, email); err != nil {
			problem(w, 503, "Verification is temporarily unavailable")
			return
		}
		if err = tx.Commit(); err != nil {
			problem(w, 503, "Verification is temporarily unavailable")
			return
		}
		if attempts+1 >= s.config.MaxAttempts {
			problem(w, 429, "Too many attempts. Request a new code")
		} else {
			problem(w, 400, "The verification code is incorrect")
		}
		return
	}
	until := now.Add(s.config.RegistrationTTL)
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO verified_registrations (token_hash,email,expires_at) VALUES (?,?,?)`, tokenHash(token), email, until); err != nil {
		problem(w, 503, "Verification is temporarily unavailable")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE email_verifications SET code_mac=NULL,verified_until=? WHERE email=?`, until, email); err != nil {
		problem(w, 503, "Verification is temporarily unavailable")
		return
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Verification is temporarily unavailable")
		return
	}
	s.setCookie(w, token)
	writeJSON(w, 200, map[string]any{"email": email, "verified": true, "expiresAt": until})
}
func (s *verificationServer) status(w http.ResponseWriter, r *http.Request) {
	token := browserToken(r)
	if token == "" {
		writeJSON(w, 200, map[string]bool{"verified": false})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var email string
	var expires, last time.Time
	err := s.db.QueryRowContext(ctx, `SELECT email,expires_at FROM verified_registrations WHERE token_hash=? AND expires_at>?`, tokenHash(token), s.now()).Scan(&email, &expires)
	if err == nil {
		writeJSON(w, 200, map[string]any{"email": email, "verified": true, "expiresAt": expires})
		return
	}
	if err != sql.ErrNoRows {
		problem(w, 503, "Verification is temporarily unavailable")
		return
	}
	var verified sql.NullTime
	err = s.db.QueryRowContext(ctx, `SELECT email,code_expires_at,last_sent_at,verified_until FROM email_verifications WHERE token_hash=?`, tokenHash(token)).Scan(&email, &expires, &last, &verified)
	if err == sql.ErrNoRows {
		writeJSON(w, 200, map[string]bool{"verified": false})
		return
	}
	if err != nil {
		problem(w, 503, "Verification is temporarily unavailable")
		return
	}
	now := s.now()
	retry := max(0, int(last.Add(s.config.ResendInterval).Sub(now).Seconds())+1)
	writeJSON(w, 200, map[string]any{"email": email, "verified": false, "codeExpired": verified.Valid || !now.Before(expires), "retryAfterSeconds": retry})
}
func validOrigin(value string) bool {
	u, e := url.Parse(value)
	return e == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.User == nil
}
