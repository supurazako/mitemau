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
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

type loginFixture struct {
	userID, credentialID []byte
	email                string
	key                  *ecdsa.PrivateKey
}

func (f loginFixture) assertion(t *testing.T, options *httptest.ResponseRecorder, origin string, uv bool, counter uint32) string {
	t.Helper()
	var data struct{ Challenge string }
	if err := json.Unmarshal(options.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	client, _ := json.Marshal(map[string]any{"type": "webauthn.get", "challenge": data.Challenge, "origin": origin, "crossOrigin": false})
	rpHash := sha256.Sum256([]byte("localhost"))
	auth := append([]byte{}, rpHash[:]...)
	flags := byte(1)
	if uv {
		flags |= 4
	}
	auth = append(auth, flags)
	auth = binary.BigEndian.AppendUint32(auth, counter)
	clientHash := sha256.Sum256(client)
	signed := append(append([]byte{}, auth...), clientHash[:]...)
	digest := sha256.Sum256(signed)
	signature, err := ecdsa.SignASN1(rand.Reader, f.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	body, _ := json.Marshal(map[string]any{"id": enc(f.credentialID), "rawId": enc(f.credentialID), "type": "public-key", "clientExtensionResults": map[string]any{}, "response": map[string]any{"clientDataJSON": enc(client), "authenticatorData": enc(auth), "signature": enc(signature), "userHandle": enc(f.userID)}})
	return string(body)
}
func TestSignInAndSessionsMySQL(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN not configured")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	setup := func(t *testing.T) (*passkeyServer, *http.ServeMux, loginFixture) {
		t.Helper()
		v := newVerificationServer(db, &recordingSender{}, testConfig())
		p, err := newPasskeyServer(v)
		if err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		p.routes(mux)
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		fixture := loginFixture{userID: make([]byte, 32), credentialID: make([]byte, 32), key: key}
		_, _ = rand.Read(fixture.userID)
		_, _ = rand.Read(fixture.credentialID)
		token, _ := newToken()
		fixture.email = "login-" + token[:20] + "@example.test"
		public, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: key.X.FillBytes(make([]byte, 32)), -3: key.Y.FillBytes(make([]byte, 32))})
		if err != nil {
			t.Fatal(err)
		}
		cred := webauthn.Credential{ID: fixture.credentialID, PublicKey: public, AttestationType: "none", Flags: webauthn.NewCredentialFlags(protocol.AuthenticatorFlags(5))}
		raw, _ := json.Marshal(cred)
		hash := sha256.Sum256(fixture.credentialID)
		now := time.Now().UTC()
		if _, err = db.Exec(`INSERT INTO accounts (id,email,created_at) VALUES (?,?,?)`, fixture.userID, fixture.email, now); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO passkey_credentials (id_hash,account_id,credential,created_at) VALUES (?,?,?,?)`, hash[:], fixture.userID, raw, now); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM accounts WHERE id=?`, fixture.userID) })
		return p, mux, fixture
	}
	begin := func(t *testing.T, mux http.Handler, old *http.Cookie) (*httptest.ResponseRecorder, *http.Cookie) {
		t.Helper()
		w := request(mux, "POST", "/login/passkey/options", "{}", old)
		requireStatus(t, w, 200)
		cookies := w.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != loginCookie {
			t.Fatal("missing challenge cookie")
		}
		cookie := cookies[0]
		t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM login_ceremonies WHERE token_hash=?`, tokenHash(cookie.Value)) })
		return w, cookie
	}
	finish := func(mux http.Handler, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
		return request(mux, "POST", "/login/passkey/finish", body, cookie)
	}
	sessionFrom := func(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
		t.Helper()
		for _, c := range w.Result().Cookies() {
			if c.Name == sessionCookie && c.MaxAge > 0 {
				return c
			}
		}
		t.Fatal("missing authenticated cookie")
		return nil
	}
	t.Run("usernameless login updates credential and creates session", func(t *testing.T) {
		_, mux, f := setup(t)
		options, cookie := begin(t, mux, nil)
		var selection struct {
			AllowCredentials []any
			UserVerification string
		}
		_ = json.Unmarshal(options.Body.Bytes(), &selection)
		if len(selection.AllowCredentials) != 0 || selection.UserVerification != "required" {
			t.Fatal("not a UV-required discoverable request")
		}
		body := f.assertion(t, options, "http://localhost:5173", true, 1)
		w := finish(mux, cookie, body)
		requireStatus(t, w, 200)
		session := sessionFrom(t, w)
		if !session.HttpOnly || session.SameSite != http.SameSiteStrictMode {
			t.Fatal("missing cookie protection")
		}
		requireStatus(t, finish(mux, cookie, body), 401)
		state := request(mux, "GET", "/session", "", session)
		if !strings.Contains(state.Body.String(), `"signedIn":true`) {
			t.Fatal("not signed in")
		}
		var raw []byte
		_ = db.QueryRow(`SELECT credential FROM passkey_credentials WHERE account_id=?`, f.userID).Scan(&raw)
		var cred webauthn.Credential
		_ = json.Unmarshal(raw, &cred)
		if cred.Authenticator.SignCount != 1 {
			t.Fatal("counter not persisted")
		}
	})
	for _, scenario := range []string{"wrong origin", "missing UV", "wrong handle", "invalid signature", "malformed JSON"} {
		t.Run(scenario, func(t *testing.T) {
			_, mux, f := setup(t)
			options, cookie := begin(t, mux, nil)
			origin := "http://localhost:5173"
			uv := true
			if scenario == "wrong origin" {
				origin = "https://evil.test"
			}
			if scenario == "missing UV" {
				uv = false
			}
			if scenario == "wrong handle" {
				f.userID = make([]byte, 32)
			}
			body := f.assertion(t, options, origin, uv, 1)
			if scenario == "invalid signature" {
				var parsed map[string]any
				_ = json.Unmarshal([]byte(body), &parsed)
				parsed["response"].(map[string]any)["signature"] = "AA"
				raw, _ := json.Marshal(parsed)
				body = string(raw)
			}
			if scenario == "malformed JSON" {
				body = `{`
			}
			w := finish(mux, cookie, body)
			requireStatus(t, w, 401)
			for _, c := range w.Result().Cookies() {
				if c.Name == sessionCookie {
					t.Fatal("failed login issued a session")
				}
			}
			requireStatus(t, finish(mux, cookie, body), 401)
		})
	}
	t.Run("expired and replaced challenges rejected", func(t *testing.T) {
		_, mux, f := setup(t)
		options, cookie := begin(t, mux, nil)
		body := f.assertion(t, options, "http://localhost:5173", true, 1)
		begin(t, mux, cookie)
		requireStatus(t, finish(mux, cookie, body), 401)
		options, cookie = begin(t, mux, cookie)
		body = f.assertion(t, options, "http://localhost:5173", true, 1)
		_, err := db.Exec(`UPDATE login_ceremonies SET expires_at=? WHERE token_hash=?`, time.Now().UTC().Add(-time.Second), tokenHash(cookie.Value))
		if err != nil {
			t.Fatal(err)
		}
		requireStatus(t, finish(mux, cookie, body), 410)
	})
	t.Run("counter regression rejected", func(t *testing.T) {
		_, mux, f := setup(t)
		options, cookie := begin(t, mux, nil)
		requireStatus(t, finish(mux, cookie, f.assertion(t, options, "http://localhost:5173", true, 2)), 200)
		options, cookie = begin(t, mux, nil)
		requireStatus(t, finish(mux, cookie, f.assertion(t, options, "http://localhost:5173", true, 1)), 401)
	})
	t.Run("concurrent finish consumes challenge once", func(t *testing.T) {
		_, mux, f := setup(t)
		options, cookie := begin(t, mux, nil)
		body := f.assertion(t, options, "http://localhost:5173", true, 1)
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
			if status == 200 {
				successes++
			} else if status != 401 {
				t.Fatalf("unexpected %d", status)
			}
		}
		if successes != 1 {
			t.Fatal("challenge consumed multiple times")
		}
	})
	t.Run("logout revokes one session and is idempotent", func(t *testing.T) {
		_, mux, f := setup(t)
		options, cookie := begin(t, mux, nil)
		a := sessionFrom(t, finish(mux, cookie, f.assertion(t, options, "http://localhost:5173", true, 1)))
		options, cookie = begin(t, mux, nil)
		b := sessionFrom(t, finish(mux, cookie, f.assertion(t, options, "http://localhost:5173", true, 2)))
		requireStatus(t, request(mux, "POST", "/logout", "{}", a), 200)
		if strings.Contains(request(mux, "GET", "/session", "", a).Body.String(), `"signedIn":true`) {
			t.Fatal("revoked cookie still works")
		}
		if !strings.Contains(request(mux, "GET", "/session", "", b).Body.String(), `"signedIn":true`) {
			t.Fatal("other browser logged out")
		}
		requireStatus(t, request(mux, "POST", "/logout", "{}", a), 200)
	})
	t.Run("sign-in rotates prior browser session", func(t *testing.T) {
		_, mux, f := setup(t)
		options, cookie := begin(t, mux, nil)
		old := sessionFrom(t, finish(mux, cookie, f.assertion(t, options, "http://localhost:5173", true, 1)))
		options, cookie = begin(t, mux, nil)
		r := httptest.NewRequest("POST", "/login/passkey/finish", strings.NewReader(f.assertion(t, options, "http://localhost:5173", true, 2)))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(cookie)
		r.AddCookie(old)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		requireStatus(t, w, 200)
		fresh := sessionFrom(t, w)
		if old.Value == fresh.Value {
			t.Fatal("session was not rotated")
		}
		if strings.Contains(request(mux, "GET", "/session", "", old).Body.String(), `"signedIn":true`) {
			t.Fatal("old session retained")
		}
	})
	t.Run("renewal is bounded and status reads do not renew", func(t *testing.T) {
		p, mux, f := setup(t)
		options, cookie := begin(t, mux, nil)
		session := sessionFrom(t, finish(mux, cookie, f.assertion(t, options, "http://localhost:5173", true, 1)))
		var idle, absolute time.Time
		err := db.QueryRow(`SELECT expires_at,absolute_expires_at FROM account_sessions WHERE token_hash=?`, tokenHash(session.Value)).Scan(&idle, &absolute)
		if err != nil {
			t.Fatal(err)
		}
		p.verification.now = func() time.Time { return idle.Add(-time.Hour) }
		request(mux, "GET", "/session", "", session)
		var unchanged time.Time
		_ = db.QueryRow(`SELECT expires_at FROM account_sessions WHERE token_hash=?`, tokenHash(session.Value)).Scan(&unchanged)
		if !unchanged.Equal(idle) {
			t.Fatal("GET renewed session")
		}
		requireStatus(t, request(mux, "POST", "/session/renew", "{}", session), 200)
		var renewed time.Time
		_ = db.QueryRow(`SELECT expires_at FROM account_sessions WHERE token_hash=?`, tokenHash(session.Value)).Scan(&renewed)
		if !renewed.After(idle) || renewed.After(absolute) {
			t.Fatal("incorrect renewal")
		}
		// Move close to the hard deadline while keeping idle expiry valid.
		near := absolute.Add(-time.Hour)
		_, err = db.Exec(`UPDATE account_sessions SET expires_at=? WHERE token_hash=?`, absolute, tokenHash(session.Value))
		if err != nil {
			t.Fatal(err)
		}
		p.verification.now = func() time.Time { return near }
		requireStatus(t, request(mux, "POST", "/session/renew", "{}", session), 200)
		_ = db.QueryRow(`SELECT expires_at FROM account_sessions WHERE token_hash=?`, tokenHash(session.Value)).Scan(&renewed)
		if !renewed.Equal(absolute) {
			t.Fatal("renewal exceeded hard deadline")
		}
		p.verification.now = func() time.Time { return absolute }
		requireStatus(t, request(mux, "POST", "/session/renew", "{}", session), 401)
		if strings.Contains(request(mux, "GET", "/session", "", session).Body.String(), `"signedIn":true`) {
			t.Fatal("absolute expiry ignored")
		}
	})
	t.Run("idle expiry and logout cannot be revived", func(t *testing.T) {
		p, mux, f := setup(t)
		options, cookie := begin(t, mux, nil)
		session := sessionFrom(t, finish(mux, cookie, f.assertion(t, options, "http://localhost:5173", true, 1)))
		p.verification.now = func() time.Time { return time.Now().UTC().Add(25 * time.Hour) }
		requireStatus(t, request(mux, "POST", "/session/renew", "{}", session), 401)
		p.verification.now = func() time.Time { return time.Now().UTC() }
		requireStatus(t, request(mux, "POST", "/session/renew", "{}", session), 401)
	})
	t.Run("cross-origin and form logout rejected", func(t *testing.T) {
		_, mux, _ := setup(t)
		for _, path := range []string{"/logout", "/session/renew", "/login/passkey/options", "/login/passkey/finish"} {
			r := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
			r.Header.Set("Origin", "https://evil.test")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			requireStatus(t, w, 403)
			r = httptest.NewRequest("POST", path, strings.NewReader("x=y"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w = httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			requireStatus(t, w, 415)
		}
	})
}
