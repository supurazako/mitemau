package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
)

func registrationResponse(t *testing.T, options *httptest.ResponseRecorder, origin string, uv bool) string {
	t.Helper()
	var parsed struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(options.Body.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: key.X.FillBytes(make([]byte, 32)), -3: key.Y.FillBytes(make([]byte, 32))})
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		t.Fatal(err)
	}
	rpHash := sha256.Sum256([]byte("localhost"))
	auth := append([]byte{}, rpHash[:]...)
	flag := byte(0x41)
	if uv {
		flag |= 0x04
	}
	auth = append(auth, flag)
	auth = append(auth, make([]byte, 4+16)...)
	auth = binary.BigEndian.AppendUint16(auth, uint16(len(id)))
	auth = append(auth, id...)
	auth = append(auth, public...)
	attestation, err := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": auth})
	if err != nil {
		t.Fatal(err)
	}
	clientData, _ := json.Marshal(map[string]any{"type": "webauthn.create", "challenge": parsed.Challenge, "origin": origin, "crossOrigin": false})
	enc := base64.RawURLEncoding.EncodeToString
	body, _ := json.Marshal(map[string]any{"id": enc(id), "rawId": enc(id), "type": "public-key", "clientExtensionResults": map[string]any{}, "response": map[string]any{"clientDataJSON": enc(clientData), "attestationObject": enc(attestation), "transports": []string{"internal"}}})
	return string(body)
}
func TestPasskeyConfig(t *testing.T) {
	t.Setenv("WEBAUTHN_RP_ID", "localhost")
	v := newVerificationServer(nil, nil, testConfig())
	p, err := newPasskeyServer(v)
	if err != nil {
		t.Fatal(err)
	}
	if p.challengeTTL != 5*time.Minute || p.sessionTTL != 24*time.Hour {
		t.Fatal("unexpected defaults")
	}
	t.Setenv("WEBAUTHN_RP_ID", "evil.test")
	if _, err := newPasskeyServer(v); err == nil {
		t.Fatal("RP mismatch accepted")
	}
}
func TestPasskeyRegistrationMySQL(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN not configured")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	setup := func(t *testing.T) (*verificationServer, *passkeyServer, *http.ServeMux, *http.Cookie, string) {
		t.Helper()
		v := newVerificationServer(db, &recordingSender{}, testConfig())
		p, err := newPasskeyServer(v)
		if err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		p.routes(mux)
		token, err := newToken()
		if err != nil {
			t.Fatal(err)
		}
		email := "passkey-" + token[:20] + "@example.test"
		_, err = db.Exec(`INSERT INTO verified_registrations (token_hash,email,expires_at) VALUES (?,?,?)`, tokenHash(token), email, time.Now().UTC().Add(30*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = db.Exec(`DELETE FROM verified_registrations WHERE email=?`, email)
			_, _ = db.Exec(`DELETE FROM accounts WHERE email=?`, email)
		})
		return v, p, mux, &http.Cookie{Name: registrationCookie, Value: token}, email
	}
	begin := func(t *testing.T, mux http.Handler, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		w := request(mux, "POST", "/registration/passkey/options", "{}", cookie)
		requireStatus(t, w, 200)
		return w
	}
	finish := func(mux http.Handler, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
		return request(mux, "POST", "/registration/passkey/finish", body, cookie)
	}
	t.Run("success atomic account credential grant consumption and session", func(t *testing.T) {
		_, p, mux, cookie, email := setup(t)
		options := begin(t, mux, cookie)
		var decoded struct {
			AuthenticatorSelection struct{ ResidentKey, UserVerification string }
			User                   struct{ ID string }
		}
		if err := json.Unmarshal(options.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.AuthenticatorSelection.ResidentKey != "required" || decoded.AuthenticatorSelection.UserVerification != "required" {
			t.Fatal("registration policy not required")
		}
		body := registrationResponse(t, options, "http://localhost:5173", true)
		w := finish(mux, cookie, body)
		requireStatus(t, w, 201)
		var session *http.Cookie
		for _, c := range w.Result().Cookies() {
			if c.Name == sessionCookie {
				session = c
			}
			if c.Name == registrationCookie && c.MaxAge != -1 {
				t.Fatal("registration cookie was not cleared")
			}
		}
		if session == nil || !session.HttpOnly || session.SameSite != http.SameSiteStrictMode {
			t.Fatal("missing session protections")
		}
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM passkey_credentials p JOIN accounts a ON a.id=p.account_id WHERE a.email=?`, email).Scan(&count); err != nil || count != 1 {
			t.Fatal("credential not persisted")
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM verified_registrations WHERE token_hash=?`, tokenHash(cookie.Value)).Scan(&count); err != nil || count != 0 {
			t.Fatal("email grant not consumed")
		}
		requireStatus(t, finish(mux, cookie, body), 401)
		w = request(mux, "GET", "/session", "", session)
		requireStatus(t, w, 200)
		if !strings.Contains(w.Body.String(), `"signedIn":true`) {
			t.Fatal("session not authenticated")
		}
		p.verification.now = func() time.Time { return time.Now().UTC().Add(25 * time.Hour) }
		w = request(mux, "GET", "/session", "", session)
		if !strings.Contains(w.Body.String(), `"signedIn":false`) {
			t.Fatal("expired session accepted")
		}
	})
	for _, scenario := range []string{"missing user verification", "wrong origin", "invalid JSON"} {
		t.Run(scenario, func(t *testing.T) {
			_, _, mux, cookie, email := setup(t)
			options := begin(t, mux, cookie)
			origin := "http://localhost:5173"
			uv := true
			if scenario == "wrong origin" {
				origin = "https://evil.test"
			}
			if scenario == "missing user verification" {
				uv = false
			}
			body := registrationResponse(t, options, origin, uv)
			if scenario == "invalid JSON" {
				body = `{`
			}
			requireStatus(t, finish(mux, cookie, body), 400)
			var count int
			_ = db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE email=?`, email).Scan(&count)
			if count != 0 {
				t.Fatal("failed verification created account")
			}
			requireStatus(t, finish(mux, cookie, body), 409)
			begin(t, mux, cookie) // email verification is reusable for a fresh ceremony
		})
	}
	t.Run("missing or expired email grant", func(t *testing.T) {
		_, _, mux, cookie, _ := setup(t)
		requireStatus(t, request(mux, "POST", "/registration/passkey/options", "{}", nil), 401)
		_, err := db.Exec(`UPDATE verified_registrations SET expires_at=? WHERE token_hash=?`, time.Now().UTC().Add(-time.Second), tokenHash(cookie.Value))
		if err != nil {
			t.Fatal(err)
		}
		requireStatus(t, request(mux, "POST", "/registration/passkey/options", "{}", cookie), 401)
	})
	t.Run("challenge expiry and replacement", func(t *testing.T) {
		_, _, mux, cookie, _ := setup(t)
		first := begin(t, mux, cookie)
		oldBody := registrationResponse(t, first, "http://localhost:5173", true)
		begin(t, mux, cookie)
		requireStatus(t, finish(mux, cookie, oldBody), 400)
		options := begin(t, mux, cookie)
		body := registrationResponse(t, options, "http://localhost:5173", true)
		_, err := db.Exec(`UPDATE registration_ceremonies SET expires_at=? WHERE registration_hash=?`, time.Now().UTC().Add(-time.Second), tokenHash(cookie.Value))
		if err != nil {
			t.Fatal(err)
		}
		requireStatus(t, finish(mux, cookie, body), 410)
		begin(t, mux, cookie)
	})
	t.Run("duplicate email rejected", func(t *testing.T) {
		_, _, mux, cookie, email := setup(t)
		options := begin(t, mux, cookie)
		body := registrationResponse(t, options, "http://localhost:5173", true)
		// Simulate another browser winning the registration race after options generation.
		_, err := db.Exec(`INSERT INTO accounts (id,email,created_at) VALUES (?,?,?)`, make([]byte, 32), email, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		requireStatus(t, finish(mux, cookie, body), 409)
		requireStatus(t, request(mux, "POST", "/registration/passkey/options", "{}", cookie), 409)
	})
	t.Run("concurrent finish creates one account and session", func(t *testing.T) {
		_, _, mux, cookie, email := setup(t)
		options := begin(t, mux, cookie)
		body := registrationResponse(t, options, "http://localhost:5173", true)
		var wg sync.WaitGroup
		statuses := make(chan int, 4)
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); statuses <- finish(mux, cookie, body).Code }()
		}
		wg.Wait()
		close(statuses)
		successes := 0
		for status := range statuses {
			if status == 201 {
				successes++
			} else if status != 401 {
				t.Fatalf("unexpected %d", status)
			}
		}
		if successes != 1 {
			t.Fatal("multiple successful registrations")
		}
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM account_sessions s JOIN accounts a ON a.id=s.account_id WHERE a.email=?`, email).Scan(&count)
		if err != nil || count != 1 {
			t.Fatal("multiple or missing sessions")
		}
	})
	t.Run("registered email is not sent another code", func(t *testing.T) {
		v, p, _, _, email := setup(t)
		_, err := db.Exec(`INSERT INTO accounts (id,email,created_at) VALUES (?,?,?)`, make([]byte, 32), email, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		v.registeredEmail = p.registeredEmail
		mux := http.NewServeMux()
		v.routes(mux)
		w := request(mux, "POST", "/registration/email", fmt.Sprintf(`{"email":%q}`, email), nil)
		requireStatus(t, w, 409)
	})
	t.Run("two verified browsers cannot create duplicate accounts", func(t *testing.T) {
		_, _, mux, first, email := setup(t)
		secondToken, err := newToken()
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Exec(`INSERT INTO verified_registrations (token_hash,email,expires_at) VALUES (?,?,?)`, tokenHash(secondToken), email, time.Now().UTC().Add(30*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		second := &http.Cookie{Name: registrationCookie, Value: secondToken}
		firstBody := registrationResponse(t, begin(t, mux, first), "http://localhost:5173", true)
		secondBody := registrationResponse(t, begin(t, mux, second), "http://localhost:5173", true)
		var wg sync.WaitGroup
		statuses := make(chan int, 2)
		for _, attempt := range []struct {
			cookie *http.Cookie
			body   string
		}{{first, firstBody}, {second, secondBody}} {
			wg.Add(1)
			go func() { defer wg.Done(); statuses <- finish(mux, attempt.cookie, attempt.body).Code }()
		}
		wg.Wait()
		close(statuses)
		successes, conflicts := 0, 0
		for status := range statuses {
			if status == 201 {
				successes++
			} else if status == 409 {
				conflicts++
			} else {
				t.Fatalf("unexpected status %d", status)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatal("email uniqueness not enforced across browsers")
		}
	})
	t.Run("storage failure rolls back account creation", func(t *testing.T) {
		_, _, mux, cookie, email := setup(t)
		body := registrationResponse(t, begin(t, mux, cookie), "http://localhost:5173", true)
		var decoded struct{ ID string }
		_ = json.Unmarshal([]byte(body), &decoded)
		id, err := base64.RawURLEncoding.DecodeString(decoded.ID)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(id)
		otherEmail := "other-" + email
		otherID := make([]byte, 32)
		otherID[0] = 1
		_, err = db.Exec(`INSERT INTO accounts (id,email,created_at) VALUES (?,?,?)`, otherID, otherEmail, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM accounts WHERE email=?`, otherEmail) })
		_, err = db.Exec(`INSERT INTO passkey_credentials (id_hash,account_id,credential,created_at) VALUES (?,?,?,?)`, hash[:], otherID, `{}`, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		requireStatus(t, finish(mux, cookie, body), 503)
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE email=?`, email).Scan(&count); err != nil || count != 0 {
			t.Fatal("partial account survived failure")
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM verified_registrations WHERE token_hash=?`, tokenHash(cookie.Value)).Scan(&count); err != nil || count != 1 {
			t.Fatal("failed transaction consumed email verification")
		}
	})

}
