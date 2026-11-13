package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingSender struct {
	mu    sync.Mutex
	codes []string
	fail  bool
}

func (s *recordingSender) Send(_ context.Context, _ string, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return errors.New("delivery failed")
	}
	s.codes = append(s.codes, code)
	return nil
}
func (s *recordingSender) last() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.codes[len(s.codes)-1]
}
func testConfig() verificationConfig {
	return verificationConfig{Secret: []byte(strings.Repeat("s", 32)), CodeTTL: 10 * time.Minute, RegistrationTTL: 30 * time.Minute, ResendInterval: time.Minute, MaxAttempts: 5, MaxSends: 1000, SendWindow: time.Hour, CleanupInterval: time.Hour, Origin: "http://localhost:5173"}
}
func request(mux http.Handler, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}
func requireStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body.String())
	}
}
func TestNormalizedEmail(t *testing.T) {
	for _, value := range []string{"bad", "Name <a@example.com>", "a@example.com\r\nBcc: other@example.com", "a@éxample.com"} {
		if _, err := normalizedEmail(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
	value, err := normalizedEmail(" Person@EXAMPLE.COM ")
	if err != nil || value != "Person@example.com" {
		t.Fatalf("normalization: %q %v", value, err)
	}
}
func TestProtectedEndpoints(t *testing.T) {
	s := newVerificationServer(nil, nil, testConfig())
	mux := http.NewServeMux()
	s.routes(mux)
	for _, tc := range []struct {
		name, origin, content, body string
		status                      int
	}{
		{"cross origin", "https://evil.test", "application/json", `{}`, 403},
		{"form", "", "application/x-www-form-urlencoded", "email=a", 415},
		{"unknown field", "", "application/json", `{"email":"a@example.com","unexpected":true}`, 400},
		{"extra body", "", "application/json", `{"email":"a@example.com"}{}`, 400},
		{"invalid email", "", "application/json", `{"email":"not an address"}`, 400},
		{"oversized", "", "application/json", `{"email":"` + strings.Repeat("a", 3000) + `"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/registration/email", strings.NewReader(tc.body))
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Content-Type", tc.content)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			requireStatus(t, w, tc.status)
		})
	}
}
func TestCodeProtection(t *testing.T) {
	s := newVerificationServer(nil, nil, testConfig())
	if string(s.codeMAC("a", "123456")) == string(s.codeMAC("b", "123456")) {
		t.Fatal("code is not bound to browser token")
	}
	for i := 0; i < 50; i++ {
		code, err := newCode()
		if err != nil || len(code) != 6 || strings.Trim(code, "0123456789") != "" {
			t.Fatal("invalid generated code")
		}
	}
}

// MYSQL_TEST_DSN must explicitly select a disposable database with migrations applied.
func TestVerificationMySQL(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN not configured")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Ping(); err != nil {
		t.Fatal(err)
	}
	setup := func(t *testing.T) (*verificationServer, *http.ServeMux, *recordingSender, string, *time.Time) {
		t.Helper()
		sender := &recordingSender{}
		s := newVerificationServer(db, sender, testConfig())
		now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
		s.now = func() time.Time { return now }
		mux := http.NewServeMux()
		s.routes(mux)
		token, e := newToken()
		if e != nil {
			t.Fatal(e)
		}
		email := "test-" + token[:24] + "@example.test"
		s.config.Secret = []byte(token)
		t.Cleanup(func() {
			_, _ = db.Exec("DELETE FROM verified_registrations WHERE email LIKE ?", "test-"+token[:24]+"%")
			_, _ = db.Exec("DELETE FROM email_verifications WHERE email LIKE ?", "test-"+token[:24]+"%")
			source, _ := s.sourceHash(httptest.NewRequest("POST", "/", nil))
			_, _ = db.Exec("DELETE FROM email_send_limits WHERE source_hash=?", source)
		})
		t.Cleanup(func() {
			if _, e := db.Exec("DELETE FROM email_verifications WHERE email=?", email); e != nil {
				t.Error(e)
			}
		})
		return s, mux, sender, email, &now
	}
	send := func(t *testing.T, mux http.Handler, email string) *http.Cookie {
		t.Helper()
		w := request(mux, "POST", "/registration/email", fmt.Sprintf(`{"email":%q}`, email), nil)
		requireStatus(t, w, 202)
		cookies := w.Result().Cookies()
		if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
			t.Fatal("incorrect registration cookie")
		}
		return cookies[0]
	}
	verify := func(mux http.Handler, cookie *http.Cookie, code string) *httptest.ResponseRecorder {
		return request(mux, "POST", "/registration/email/verify", fmt.Sprintf(`{"code":%q}`, code), cookie)
	}
	t.Run("success restart replay browser binding and expiry", func(t *testing.T) {
		s, mux, sender, email, now := setup(t)
		cookie := send(t, mux, email)
		code := sender.last()
		var stored []byte
		if err := db.QueryRow("SELECT code_mac FROM email_verifications WHERE email=?", email).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if len(stored) != 32 || string(stored) == code {
			t.Fatal("code stored in plaintext")
		}
		requireStatus(t, verify(mux, nil, code), 400)
		alien := *cookie
		alien.Value = strings.Repeat("0", 64)
		requireStatus(t, verify(mux, &alien, code), 400)
		requireStatus(t, verify(mux, cookie, code), 200)
		requireStatus(t, verify(mux, cookie, code), 409)
		// A new server instance restores state from the same DB and cookie.
		restarted := newVerificationServer(db, sender, s.config)
		restarted.now = s.now
		other := http.NewServeMux()
		restarted.routes(other)
		w := request(other, "GET", "/registration/email", "", cookie)
		requireStatus(t, w, 200)
		var status struct{ Verified bool }
		if e := json.Unmarshal(w.Body.Bytes(), &status); e != nil || !status.Verified {
			t.Fatal("verified state did not survive restart")
		}
		*now = now.Add(30 * time.Minute)
		w = request(other, "GET", "/registration/email", "", cookie)
		if e := json.Unmarshal(w.Body.Bytes(), &status); e != nil || status.Verified {
			t.Fatal("expired registration remained verified")
		}
	})
	t.Run("wrong codes exhaust attempt limit", func(t *testing.T) {
		_, mux, sender, email, _ := setup(t)
		cookie := send(t, mux, email)
		correct := sender.last()
		wrong := "000000"
		if correct == wrong {
			wrong = "111111"
		}
		for i := 0; i < 5; i++ {
			expected := 400
			if i == 4 {
				expected = 429
			}
			requireStatus(t, verify(mux, cookie, wrong), expected)
		}
		requireStatus(t, verify(mux, cookie, correct), 429)
	})
	t.Run("expiration", func(t *testing.T) {
		_, mux, sender, email, now := setup(t)
		cookie := send(t, mux, email)
		*now = now.Add(10 * time.Minute)
		requireStatus(t, verify(mux, cookie, sender.last()), 410)
	})
	t.Run("resend cooldown and invalidation", func(t *testing.T) {
		_, mux, sender, email, now := setup(t)
		old := send(t, mux, email)
		oldCode := sender.last()
		w := request(mux, "POST", "/registration/email", fmt.Sprintf(`{"email":%q}`, email), old)
		requireStatus(t, w, 429)
		if w.Header().Get("Retry-After") == "" {
			t.Fatal("missing retry hint")
		}
		*now = now.Add(time.Minute)
		fresh := send(t, mux, email)
		requireStatus(t, verify(mux, old, oldCode), 400)
		requireStatus(t, verify(mux, fresh, sender.last()), 200)
	})
	t.Run("failed delivery permits retry", func(t *testing.T) {
		_, mux, sender, email, _ := setup(t)
		sender.fail = true
		w := request(mux, "POST", "/registration/email", fmt.Sprintf(`{"email":%q}`, email), nil)
		requireStatus(t, w, 502)
		if len(w.Result().Cookies()) != 0 {
			t.Fatal("failed send set cookie")
		}
		sender.fail = false
		send(t, mux, email)
	})
	t.Run("concurrent requests only send once", func(t *testing.T) {
		_, mux, sender, email, _ := setup(t)
		var wg sync.WaitGroup
		statuses := make(chan int, 4)
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				statuses <- request(mux, "POST", "/registration/email", fmt.Sprintf(`{"email":%q}`, email), nil).Code
			}()
		}
		wg.Wait()
		close(statuses)
		successes := 0
		for status := range statuses {
			if status == 202 {
				successes++
			} else if status != 429 {
				t.Fatalf("unexpected status %d", status)
			}
		}
		if successes != 1 || len(sender.codes) != 1 {
			t.Fatal("concurrent send was not limited")
		}
	})
	t.Run("concurrent verification consumes once", func(t *testing.T) {
		_, mux, sender, email, _ := setup(t)
		cookie := send(t, mux, email)
		code := sender.last()
		var wg sync.WaitGroup
		statuses := make(chan int, 4)
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); statuses <- verify(mux, cookie, code).Code }()
		}
		wg.Wait()
		close(statuses)
		successes := 0
		for status := range statuses {
			if status == 200 {
				successes++
			} else if status != 409 {
				t.Fatalf("unexpected status %d", status)
			}
		}
		if successes != 1 {
			t.Fatal("code consumed more than once")
		}
	})
	t.Run("verified browser survives another browser resend", func(t *testing.T) {
		_, mux, sender, email, now := setup(t)
		first := send(t, mux, email)
		requireStatus(t, verify(mux, first, sender.last()), 200)
		*now = now.Add(time.Minute)
		second := send(t, mux, email)
		w := request(mux, "GET", "/registration/email", "", first)
		var state struct{ Verified bool }
		_ = json.Unmarshal(w.Body.Bytes(), &state)
		if !state.Verified {
			t.Fatal("resend revoked an already verified browser")
		}
		requireStatus(t, verify(mux, second, sender.last()), 200)
		w = request(mux, "GET", "/registration/email", "", first)
		_ = json.Unmarshal(w.Body.Bytes(), &state)
		if !state.Verified {
			t.Fatal("second verification revoked first browser")
		}
	})
	t.Run("source limit covers different recipients and ignores forged forwarding", func(t *testing.T) {
		s, mux, _, email, now := setup(t)
		s.config.MaxSends = 2
		send(t, mux, email)
		send(t, mux, strings.Replace(email, "@", "+second@", 1))
		r := httptest.NewRequest("POST", "/registration/email", strings.NewReader(fmt.Sprintf(`{"email":%q}`, strings.Replace(email, "@", "+third@", 1))))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Forwarded-For", "198.51.100.123")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		requireStatus(t, w, 429)
		if w.Header().Get("Retry-After") == "" {
			t.Fatal("missing source limit retry hint")
		}
		*now = now.Add(time.Hour)
		send(t, mux, strings.Replace(email, "@", "+third@", 1))
	})
	t.Run("cleanup removes expired state and preserves live registrations", func(t *testing.T) {
		s, mux, sender, email, now := setup(t)
		first := send(t, mux, email)
		requireStatus(t, verify(mux, first, sender.last()), 200)
		*now = now.Add(29 * time.Minute)
		second := send(t, mux, email)
		requireStatus(t, verify(mux, second, sender.last()), 200)
		*now = now.Add(2 * time.Minute)
		if err := s.cleanup(context.Background()); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM verified_registrations WHERE email=?", email).Scan(&count); err != nil || count != 1 {
			t.Fatalf("live registration not retained: %d, %v", count, err)
		}
		var state struct{ Verified bool }
		w := request(mux, "GET", "/registration/email", "", first)
		_ = json.Unmarshal(w.Body.Bytes(), &state)
		if state.Verified {
			t.Fatal("expired registration remains usable")
		}
		w = request(mux, "GET", "/registration/email", "", second)
		_ = json.Unmarshal(w.Body.Bytes(), &state)
		if !state.Verified {
			t.Fatal("live registration removed")
		}
		*now = now.Add(2 * time.Hour)
		if err := s.cleanup(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"email_verifications", "verified_registrations"} {
			if err := db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE email=?", email).Scan(&count); err != nil || count != 0 {
				t.Fatalf("expired rows remain in %s", table)
			}
		}
		source, _ := s.sourceHash(httptest.NewRequest("POST", "/", nil))
		if err := db.QueryRow("SELECT COUNT(*) FROM email_send_limits WHERE source_hash=?", source).Scan(&count); err != nil || count != 0 {
			t.Fatal("expired limiter row remains")
		}
	})

}

func TestSourceIdentity(t *testing.T) {
	s := newVerificationServer(nil, nil, testConfig())
	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	base, err := s.sourceHash(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.10, 198.51.100.2")
	got, _ := s.sourceHash(r)
	if string(base) != string(got) {
		t.Fatal("untrusted forwarding changed source")
	}
	s.config.TrustLoopbackProxy = true
	got, _ = s.sourceHash(r)
	direct := httptest.NewRequest("POST", "/", nil)
	direct.RemoteAddr = "198.51.100.2:1234"
	want, _ := s.sourceHash(direct)
	if string(got) != string(want) {
		t.Fatal("did not use proxy-appended rightmost IP")
	}
	direct.Header.Set("X-Forwarded-For", "203.0.113.99")
	got, _ = s.sourceHash(direct)
	if string(got) != string(want) {
		t.Fatal("trusted a non-loopback proxy")
	}
}
