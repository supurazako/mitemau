package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBackupPasskeysMySQL(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN not configured")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	setup := func(t *testing.T) (*passkeyServer, *http.ServeMux, loginFixture, *http.Cookie) {
		t.Helper()
		v := newVerificationServer(db, &recordingSender{}, testConfig())
		p, err := newPasskeyServer(v)
		if err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		p.routes(mux)
		f := seedLoginFixture(t, db)
		token, err := newToken()
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		if _, err = db.Exec(`INSERT INTO account_sessions (token_hash,account_id,expires_at,absolute_expires_at) VALUES (?,?,?,?)`, tokenHash(token), f.userID, now.Add(24*time.Hour), now.Add(7*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
		return p, mux, f, &http.Cookie{Name: sessionCookie, Value: token}
	}
	begin := func(t *testing.T, mux http.Handler, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		w := request(mux, "POST", "/account/passkeys/verify/options", "{}", cookie)
		requireStatus(t, w, 200)
		return w
	}
	verify := func(t *testing.T, mux http.Handler, cookie *http.Cookie, f loginFixture) *httptest.ResponseRecorder {
		t.Helper()
		options := begin(t, mux, cookie)
		w := request(mux, "POST", "/account/passkeys/verify/finish", f.assertion(t, options, "http://localhost:5173", true, 1), cookie)
		requireStatus(t, w, 200)
		return w
	}
	count := func(t *testing.T, f loginFixture, want int) {
		t.Helper()
		var got int
		if err := db.QueryRow(`SELECT COUNT(*) FROM passkey_credentials WHERE account_id=?`, f.userID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("credentials=%d want %d", got, want)
		}
	}
	login := func(t *testing.T, mux http.Handler, f loginFixture, counter uint32) {
		t.Helper()
		options := request(mux, "POST", "/login/passkey/options", "{}", nil)
		requireStatus(t, options, 200)
		cookie := options.Result().Cookies()[0]
		w := request(mux, "POST", "/login/passkey/finish", f.assertion(t, options, "http://localhost:5173", true, counter), cookie)
		requireStatus(t, w, 200)
	}
	t.Run("fresh verification adds same account credential and both sign in", func(t *testing.T) {
		_, mux, f, cookie := setup(t)
		requireStatus(t, request(mux, "POST", "/account/passkeys/finish", "{}", cookie), 409)
		options := begin(t, mux, cookie)
		var requestOptions struct {
			UserVerification string
			AllowCredentials []struct{ ID string }
		}
		if err := json.Unmarshal(options.Body.Bytes(), &requestOptions); err != nil {
			t.Fatal(err)
		}
		if requestOptions.UserVerification != "required" || len(requestOptions.AllowCredentials) != 1 || requestOptions.AllowCredentials[0].ID != base64.RawURLEncoding.EncodeToString(f.credentialID) {
			t.Fatal("reauthentication must allow only own credential with UV required")
		}
		assertion := f.assertion(t, options, "http://localhost:5173", true, 1)
		creation := request(mux, "POST", "/account/passkeys/verify/finish", assertion, cookie)
		requireStatus(t, creation, 200)
		var parsed struct {
			User                   struct{ ID string }
			ExcludeCredentials     []struct{ ID string }
			AuthenticatorSelection struct{ UserVerification, ResidentKey string }
		}
		if err := json.Unmarshal(creation.Body.Bytes(), &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed.User.ID != base64.RawURLEncoding.EncodeToString(f.userID) || len(parsed.ExcludeCredentials) != 1 || parsed.ExcludeCredentials[0].ID != base64.RawURLEncoding.EncodeToString(f.credentialID) || parsed.AuthenticatorSelection.UserVerification != "required" || parsed.AuthenticatorSelection.ResidentKey != "required" {
			t.Fatal("incorrect additional registration options")
		}
		// Replaying verification cannot consume the newly authorized creation step.
		requireStatus(t, request(mux, "POST", "/account/passkeys/verify/finish", assertion, cookie), 409)
		body, additional := registrationFixtureResponse(t, creation, "http://localhost:5173", true)
		if !bytes.Equal(additional.userID, f.userID) {
			t.Fatal("account changed")
		}
		requireStatus(t, request(mux, "POST", "/account/passkeys/finish", body, cookie), 201)
		requireStatus(t, request(mux, "POST", "/account/passkeys/finish", body, cookie), 409)
		count(t, f, 2)
		login(t, mux, f, 2)
		login(t, mux, additional, 1)
		var pending int
		if err := db.QueryRow(`SELECT COUNT(*) FROM backup_passkey_ceremonies WHERE session_hash=?`, tokenHash(cookie.Value)).Scan(&pending); err != nil || pending != 0 {
			t.Fatal("completed authorization retained")
		}
	})
	for _, scenario := range []string{"missing UV", "wrong origin", "other account", "malformed JSON", "invalid signature", "replaced challenge", "expired challenge", "counter regression"} {
		t.Run("reauthentication "+scenario, func(t *testing.T) {
			_, mux, f, cookie := setup(t)
			options := begin(t, mux, cookie)
			origin := "http://localhost:5173"
			uv := true
			counter := uint32(1)
			if scenario == "missing UV" {
				uv = false
			}
			if scenario == "wrong origin" {
				origin = "https://evil.test"
			}
			if scenario == "other account" {
				f2 := seedLoginFixture(t, db)
				f.key = f2.key
				f.credentialID = f2.credentialID
				f.userID = f2.userID
			}
			body := f.assertion(t, options, origin, uv, counter)
			if scenario == "malformed JSON" {
				body = "{"
			}
			if scenario == "invalid signature" {
				var parsed map[string]any
				if err := json.Unmarshal([]byte(body), &parsed); err != nil {
					t.Fatal(err)
				}
				parsed["response"].(map[string]any)["signature"] = "AA"
				raw, _ := json.Marshal(parsed)
				body = string(raw)
			}
			if scenario == "replaced challenge" {
				begin(t, mux, cookie)
			}
			expected := 401
			if scenario == "expired challenge" {
				if _, err = db.Exec(`UPDATE backup_passkey_ceremonies SET expires_at=? WHERE session_hash=?`, time.Now().UTC().Add(-time.Second), tokenHash(cookie.Value)); err != nil {
					t.Fatal(err)
				}
				expected = 410
			}
			if scenario == "counter regression" {
				valid := request(mux, "POST", "/account/passkeys/verify/finish", f.assertion(t, options, origin, true, 2), cookie)
				requireStatus(t, valid, 200)
				options = begin(t, mux, cookie)
				body = f.assertion(t, options, origin, true, 1)
			}
			requireStatus(t, request(mux, "POST", "/account/passkeys/verify/finish", body, cookie), expected)
			requireStatus(t, request(mux, "POST", "/account/passkeys/finish", "{}", cookie), 409)
		})
	}
	for _, scenario := range []string{"missing UV", "wrong origin", "malformed JSON", "expired challenge", "session expired", "absolute session expired", "logged out", "different session", "wrong challenge"} {
		t.Run("registration "+scenario, func(t *testing.T) {
			_, mux, f, cookie := setup(t)
			creation := verify(t, mux, cookie, f)
			origin := "http://localhost:5173"
			uv := scenario != "missing UV"
			if scenario == "wrong origin" {
				origin = "https://evil.test"
			}
			body := registrationResponse(t, creation, origin, uv)
			expected := 400
			switch scenario {
			case "malformed JSON":
				body = "{"
			case "expired challenge":
				if _, err = db.Exec(`UPDATE backup_passkey_ceremonies SET expires_at=? WHERE session_hash=?`, time.Now().UTC().Add(-time.Second), tokenHash(cookie.Value)); err != nil {
					t.Fatal(err)
				}
				expected = 410
			case "session expired":
				if _, err = db.Exec(`UPDATE account_sessions SET expires_at=? WHERE token_hash=?`, time.Now().UTC().Add(-time.Second), tokenHash(cookie.Value)); err != nil {
					t.Fatal(err)
				}
				expected = 401
			case "absolute session expired":
				if _, err = db.Exec(`UPDATE account_sessions SET absolute_expires_at=? WHERE token_hash=?`, time.Now().UTC().Add(-time.Second), tokenHash(cookie.Value)); err != nil {
					t.Fatal(err)
				}
				expected = 401
			case "logged out":
				requireStatus(t, request(mux, "POST", "/logout", "{}", cookie), 200)
				expected = 401
			case "different session":
				token, _ := newToken()
				now := time.Now().UTC()
				if _, err = db.Exec(`INSERT INTO account_sessions (token_hash,account_id,expires_at,absolute_expires_at) VALUES (?,?,?,?)`, tokenHash(token), f.userID, now.Add(time.Hour), now.Add(24*time.Hour)); err != nil {
					t.Fatal(err)
				}
				cookie = &http.Cookie{Name: sessionCookie, Value: token}
				expected = 409
			case "wrong challenge":
				other := httptest.NewRecorder()
				other.WriteString(`{"challenge":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`)
				body = registrationResponse(t, other, origin, true)
			}
			requireStatus(t, request(mux, "POST", "/account/passkeys/finish", body, cookie), expected)
			count(t, f, 1)
			login(t, mux, f, 2)
		})
	}
	t.Run("concurrent verification authorizes one registration", func(t *testing.T) {
		_, mux, f, cookie := setup(t)
		options := begin(t, mux, cookie)
		body := f.assertion(t, options, "http://localhost:5173", true, 1)
		results := make(chan *httptest.ResponseRecorder, 4)
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results <- request(mux, "POST", "/account/passkeys/verify/finish", body, cookie)
			}()
		}
		wg.Wait()
		close(results)
		var creation *httptest.ResponseRecorder
		for w := range results {
			if w.Code == 200 {
				if creation != nil {
					t.Fatal("verification succeeded twice")
				}
				creation = w
			} else {
				requireStatus(t, w, 409)
			}
		}
		if creation == nil {
			t.Fatal("no verification succeeded")
		}
		requireStatus(t, request(mux, "POST", "/account/passkeys/finish", registrationResponse(t, creation, "http://localhost:5173", true), cookie), 201)
		count(t, f, 2)
	})
	t.Run("concurrent completion adds only one credential", func(t *testing.T) {
		_, mux, f, cookie := setup(t)
		creation := verify(t, mux, cookie, f)
		body := registrationResponse(t, creation, "http://localhost:5173", true)
		statuses := make(chan int, 4)
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				statuses <- request(mux, "POST", "/account/passkeys/finish", body, cookie).Code
			}()
		}
		wg.Wait()
		close(statuses)
		success := 0
		for status := range statuses {
			if status == 201 {
				success++
			} else if status != 409 {
				t.Fatalf("unexpected %d", status)
			}
		}
		if success != 1 {
			t.Fatal("authorization used multiple times")
		}
		count(t, f, 2)
	})
	t.Run("duplicate credential rejected without replacing existing owner", func(t *testing.T) {
		_, mux, f, cookie := setup(t)
		creation := verify(t, mux, cookie, f)
		body, additional := registrationFixtureResponse(t, creation, "http://localhost:5173", true)
		// A storage-level duplicate must not update or reassign an existing credential.
		other := seedLoginFixture(t, db)
		// Move the conflicting fixture's ID hash only; this row is never authenticated.
		// Hash the raw credential ID just as production does.
		idHash := sha256.Sum256(additional.credentialID)
		if _, err = db.Exec(`UPDATE passkey_credentials SET id_hash=? WHERE account_id=?`, idHash[:], other.userID); err != nil {
			t.Fatal(err)
		}
		requireStatus(t, request(mux, "POST", "/account/passkeys/finish", body, cookie), 409)
		count(t, f, 1)
		count(t, other, 1)
		requireStatus(t, request(mux, "POST", "/account/passkeys/finish", body, cookie), 409)
	})
	t.Run("missing session and cross origin are rejected", func(t *testing.T) {
		_, mux, _, cookie := setup(t)
		for _, path := range []string{"/account/passkeys/verify/options", "/account/passkeys/verify/finish", "/account/passkeys/finish"} {
			requireStatus(t, request(mux, "POST", path, "{}", nil), 401)
			for _, contentType := range []string{"application/json", "application/x-www-form-urlencoded"} {
				r := httptest.NewRequest("POST", path, strings.NewReader("{}"))
				r.Header.Set("Origin", "https://evil.test")
				r.Header.Set("Content-Type", contentType)
				r.AddCookie(cookie)
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				requireStatus(t, w, 403)
			}
		}
	})
}
