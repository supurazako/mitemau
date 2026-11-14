package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type recoveryFixture struct {
	p       *passkeyServer
	mux     http.Handler
	account loginFixture
	codes   []string
	session *http.Cookie
	sender  *recordingSender
}

func recoveryStart(t *testing.T, f recoveryFixture, code string) (*httptest.ResponseRecorder, *http.Cookie) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": f.account.email, "recoveryCode": code})
	w := request(f.mux, "POST", "/recovery/email", string(body), nil)
	requireStatus(t, w, 202)
	return w, w.Result().Cookies()[0]
}
func recoveryVerify(t *testing.T, f recoveryFixture, cookie *http.Cookie) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"code": f.sender.last()})
	requireStatus(t, request(f.mux, "POST", "/recovery/email/verify", string(body), cookie), 200)
}
func recoveryOptions(t *testing.T, f recoveryFixture, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	w := request(f.mux, "POST", "/recovery/passkey/options", "{}", cookie)
	requireStatus(t, w, 200)
	return w
}
func TestRecoveryCodeFormat(t *testing.T) {
	a, err := recoveryCodeHash("0000-1111-2222-3333-4444-5555-aaaa-bbbb")
	if err != nil {
		t.Fatal(err)
	}
	b, err := recoveryCodeHash(" 000011112222333344445555AAAABBBB ")
	if err != nil || string(a) != string(b) {
		t.Fatal("code normalization mismatch")
	}
	for _, invalid := range []string{"", "123456", "not-a-recovery-code", strings.Repeat("0", 33)} {
		if _, err := recoveryCodeHash(invalid); err == nil {
			t.Fatal("accepted invalid code")
		}
	}
}
func TestAccountRecoveryMySQL(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN not configured")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	setup := func(t *testing.T) recoveryFixture {
		t.Helper()
		sender := &recordingSender{}
		v := newVerificationServer(db, sender, testConfig())
		p, err := newPasskeyServer(v)
		if err != nil {
			t.Fatal(err)
		}
		a := seedLoginFixture(t, db)
		mux := http.NewServeMux()
		p.routes(mux)
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.RemoteAddr = net.JoinHostPort(net.IP(a.userID[:16]).String(), "1234")
			mux.ServeHTTP(w, r)
		})
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		codes, err := replaceRecoveryCodes(t.Context(), tx, a.userID)
		if err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		token, _ := newToken()
		now := time.Now().UTC()
		if _, err = db.Exec(`INSERT INTO account_sessions (token_hash,account_id,expires_at,absolute_expires_at) VALUES (?,?,?,?)`, tokenHash(token), a.userID, now.Add(24*time.Hour), now.Add(7*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
		return recoveryFixture{p: p, mux: handler, account: a, codes: codes, session: &http.Cookie{Name: sessionCookie, Value: token}, sender: sender}
	}
	count := func(t *testing.T, table string, id []byte, want int) {
		t.Helper()
		var got int
		if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE account_id=?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s=%d want %d", table, got, want)
		}
	}
	assertOriginalWorks := func(t *testing.T, f recoveryFixture) {
		t.Helper()
		w := request(f.mux, "GET", "/session", "", f.session)
		if !strings.Contains(w.Body.String(), `"signedIn":true`) {
			t.Fatal("original session revoked before recovery success")
		}
		count(t, "passkey_credentials", f.account.userID, 1)
		count(t, "recovery_codes", f.account.userID, 10)
	}
	t.Run("successful recovery rotates all credentials sessions codes and grants", func(t *testing.T) {
		f := setup(t)
		// A second browser's session must also be revoked.
		token, _ := newToken()
		now := time.Now().UTC()
		if _, err := db.Exec(`INSERT INTO account_sessions (token_hash,account_id,expires_at,absolute_expires_at) VALUES (?,?,?,?)`, tokenHash(token), f.account.userID, now.Add(time.Hour), now.Add(24*time.Hour)); err != nil {
			t.Fatal(err)
		}
		pendingLogin := request(f.mux, "POST", "/login/passkey/options", "{}", nil)
		requireStatus(t, pendingLogin, 200)
		pendingAssertion := f.account.assertion(t, pendingLogin, "http://localhost:5173", true, 1)
		_, cookie := recoveryStart(t, f, f.codes[0])
		recoveryVerify(t, f, cookie)
		requireStatus(t, request(f.mux, "POST", "/recovery/email/verify", `{"code":"123456"}`, cookie), 409)
		options := recoveryOptions(t, f, cookie)
		body, additional := registrationFixtureResponse(t, options, "http://localhost:5173", true)
		assertOriginalWorks(t, f)
		w := request(f.mux, "POST", "/recovery/passkey/finish", body, cookie)
		requireStatus(t, w, 200)
		var result recoverySuccess
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if !result.SignedIn || result.Email != f.account.email || len(result.RecoveryCodes) != 10 {
			t.Fatal("missing recovered session or replacement codes")
		}
		count(t, "passkey_credentials", f.account.userID, 1)
		count(t, "account_sessions", f.account.userID, 1)
		count(t, "recovery_codes", f.account.userID, 10)
		count(t, "recovery_requests", f.account.userID, 0)
		for _, old := range f.codes {
			hash, _ := recoveryCodeHash(old)
			var n int
			if err := db.QueryRow(`SELECT COUNT(*) FROM recovery_codes WHERE code_hash=?`, hash).Scan(&n); err != nil || n != 0 {
				t.Fatal("old recovery code retained")
			}
		}
		if strings.Contains(request(f.mux, "GET", "/session", "", f.session).Body.String(), `"signedIn":true`) {
			t.Fatal("old session survived")
		}
		if strings.Contains(request(f.mux, "GET", "/session", "", &http.Cookie{Name: sessionCookie, Value: token}).Body.String(), `"signedIn":true`) {
			t.Fatal("other browser session survived")
		}
		requireStatus(t, request(f.mux, "POST", "/recovery/passkey/finish", body, cookie), 401)
		// Both a newly started and an already-open old-key login must fail after recovery.
		requireStatus(t, request(f.mux, "POST", "/login/passkey/finish", pendingAssertion, pendingLogin.Result().Cookies()[0]), 401)
		loginOptions := request(f.mux, "POST", "/login/passkey/options", "{}", nil)
		requireStatus(t, loginOptions, 200)
		requireStatus(t, request(f.mux, "POST", "/login/passkey/finish", f.account.assertion(t, loginOptions, "http://localhost:5173", true, 1), loginOptions.Result().Cookies()[0]), 401)
		loginOptions = request(f.mux, "POST", "/login/passkey/options", "{}", nil)
		requireStatus(t, request(f.mux, "POST", "/login/passkey/finish", additional.assertion(t, loginOptions, "http://localhost:5173", true, 1), loginOptions.Result().Cookies()[0]), 200)
		for _, c := range w.Result().Cookies() {
			if c.Name == sessionCookie && c.MaxAge > 0 {
				if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
					t.Fatal("weak session cookie")
				}
			}
		}
	})
	t.Run("offline code or email alone cannot recover", func(t *testing.T) {
		f := setup(t)
		for _, body := range []string{fmt.Sprintf(`{"email":%q,"recoveryCode":""}`, f.account.email), fmt.Sprintf(`{"email":%q,"recoveryCode":"00000000000000000000000000000000"}`, f.account.email), fmt.Sprintf(`{"email":"unknown@example.test","recoveryCode":%q}`, f.codes[0])} {
			requireStatus(t, request(f.mux, "POST", "/recovery/email", body, nil), 400)
		}
		if len(f.sender.codes) != 0 {
			t.Fatal("invalid offline code sent email")
		}
		_, cookie := recoveryStart(t, f, f.codes[0])
		requireStatus(t, request(f.mux, "POST", "/recovery/passkey/options", "{}", cookie), 401)
		requireStatus(t, request(f.mux, "POST", "/recovery/passkey/finish", "{}", cookie), 401)
		requireStatus(t, request(f.mux, "POST", "/recovery/email/verify", `{"code":"123456"}`, nil), 401)
		assertOriginalWorks(t, f)
	})
	t.Run("email attempts expiry resend and browser binding", func(t *testing.T) {
		f := setup(t)
		_, cookie := recoveryStart(t, f, f.codes[0])
		valid := f.sender.last()
		wrong := "000000"
		if wrong == valid {
			wrong = "111111"
		}
		for i := 0; i < 5; i++ {
			status := 400
			if i == 4 {
				status = 429
			}
			requireStatus(t, request(f.mux, "POST", "/recovery/email/verify", fmt.Sprintf(`{"code":%q}`, wrong), cookie), status)
		}
		requireStatus(t, request(f.mux, "POST", "/recovery/email/verify", fmt.Sprintf(`{"code":%q}`, valid), cookie), 429)
		body := fmt.Sprintf(`{"email":%q,"recoveryCode":%q}`, f.account.email, f.codes[0])
		requireStatus(t, request(f.mux, "POST", "/recovery/email", body, cookie), 429)
		if _, err := db.Exec(`UPDATE recovery_delivery_limits SET last_sent_at=? WHERE account_id=?`, time.Now().UTC().Add(-2*time.Minute), f.account.userID); err != nil {
			t.Fatal(err)
		}
		resent := request(f.mux, "POST", "/recovery/email", body, cookie)
		requireStatus(t, resent, 202)
		requireStatus(t, request(f.mux, "POST", "/recovery/email/verify", fmt.Sprintf(`{"code":%q}`, valid), cookie), 401)
		newer := resent.Result().Cookies()[0]
		if _, err := db.Exec(`UPDATE recovery_requests SET expires_at=? WHERE token_hash=?`, time.Now().UTC().Add(-time.Second), tokenHash(newer.Value)); err != nil {
			t.Fatal(err)
		}
		requireStatus(t, request(f.mux, "POST", "/recovery/email/verify", fmt.Sprintf(`{"code":%q}`, f.sender.last()), newer), 410)
		assertOriginalWorks(t, f)
	})
	for _, scenario := range []string{"missing UV", "wrong origin", "malformed JSON", "expired challenge", "expired grant", "replaced challenge"} {
		t.Run("registration "+scenario, func(t *testing.T) {
			f := setup(t)
			_, cookie := recoveryStart(t, f, f.codes[0])
			recoveryVerify(t, f, cookie)
			options := recoveryOptions(t, f, cookie)
			origin := "http://localhost:5173"
			if scenario == "wrong origin" {
				origin = "https://evil.test"
			}
			body := registrationResponse(t, options, origin, scenario != "missing UV")
			expected := 400
			switch scenario {
			case "malformed JSON":
				body = "{"
			case "expired challenge":
				_, err = db.Exec(`UPDATE recovery_requests SET challenge_expires_at=? WHERE token_hash=?`, time.Now().UTC().Add(-time.Second), tokenHash(cookie.Value))
				expected = 410
			case "expired grant":
				_, err = db.Exec(`UPDATE recovery_requests SET expires_at=? WHERE token_hash=?`, time.Now().UTC().Add(-time.Second), tokenHash(cookie.Value))
				expected = 410
			case "replaced challenge":
				recoveryOptions(t, f, cookie)
			}
			if err != nil {
				t.Fatal(err)
			}
			requireStatus(t, request(f.mux, "POST", "/recovery/passkey/finish", body, cookie), expected)
			assertOriginalWorks(t, f)
			if scenario != "expired grant" {
				requireStatus(t, request(f.mux, "POST", "/recovery/passkey/finish", body, cookie), 409)
				recoveryOptions(t, f, cookie)
			}
		})
	}
	t.Run("concurrent recovery succeeds once", func(t *testing.T) {
		f := setup(t)
		_, cookie := recoveryStart(t, f, f.codes[0])
		recoveryVerify(t, f, cookie)
		options := recoveryOptions(t, f, cookie)
		body := registrationResponse(t, options, "http://localhost:5173", true)
		var wg sync.WaitGroup
		statuses := make(chan int, 4)
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				statuses <- request(f.mux, "POST", "/recovery/passkey/finish", body, cookie).Code
			}()
		}
		wg.Wait()
		close(statuses)
		success := 0
		for status := range statuses {
			if status == 200 {
				success++
			} else if status != 401 {
				t.Fatalf("unexpected %d", status)
			}
		}
		if success != 1 {
			t.Fatal("recovery completed multiple times")
		}
		count(t, "account_sessions", f.account.userID, 1)
	})
	t.Run("code replacement requires fresh UV and invalidates pending recovery", func(t *testing.T) {
		f := setup(t)
		_, recovery := recoveryStart(t, f, f.codes[0])
		recoveryVerify(t, f, recovery)
		requireStatus(t, request(f.mux, "POST", "/account/recovery-codes/finish", "{}", f.session), 409)
		options := request(f.mux, "POST", "/account/recovery-codes/options", "{}", f.session)
		requireStatus(t, options, 200)
		body := f.account.assertion(t, options, "http://localhost:5173", false, 1)
		requireStatus(t, request(f.mux, "POST", "/account/recovery-codes/finish", body, f.session), 401)
		assertOriginalWorks(t, f)
		options = request(f.mux, "POST", "/account/recovery-codes/options", "{}", f.session)
		body = f.account.assertion(t, options, "http://localhost:5173", true, 1)
		result := request(f.mux, "POST", "/account/recovery-codes/finish", body, f.session)
		requireStatus(t, result, 200)
		var codes struct{ RecoveryCodes []string }
		if err := json.Unmarshal(result.Body.Bytes(), &codes); err != nil || len(codes.RecoveryCodes) != 10 {
			t.Fatal("missing replacement codes")
		}
		requireStatus(t, request(f.mux, "POST", "/account/recovery-codes/finish", body, f.session), 409)
		requireStatus(t, request(f.mux, "POST", "/recovery/passkey/options", "{}", recovery), 401)
		assertOriginalWorks(t, f)
	})
	t.Run("backup enrollment cannot authorize recovery code rotation", func(t *testing.T) {
		f := setup(t)
		options := request(f.mux, "POST", "/account/passkeys/verify/options", "{}", f.session)
		requireStatus(t, options, 200)
		body := f.account.assertion(t, options, "http://localhost:5173", true, 1)
		requireStatus(t, request(f.mux, "POST", "/account/recovery-codes/finish", body, f.session), 409)
		requireStatus(t, request(f.mux, "POST", "/account/passkeys/verify/finish", body, f.session), 200)
		requireStatus(t, request(f.mux, "POST", "/account/recovery-codes/finish", body, f.session), 409)
		assertOriginalWorks(t, f)
	})
	t.Run("expired session cannot replace recovery codes", func(t *testing.T) {
		f := setup(t)
		options := request(f.mux, "POST", "/account/recovery-codes/options", "{}", f.session)
		requireStatus(t, options, 200)
		body := f.account.assertion(t, options, "http://localhost:5173", true, 1)
		if _, err := db.Exec(`UPDATE account_sessions SET expires_at=? WHERE token_hash=?`, time.Now().UTC().Add(-time.Second), tokenHash(f.session.Value)); err != nil {
			t.Fatal(err)
		}
		requireStatus(t, request(f.mux, "POST", "/account/recovery-codes/finish", body, f.session), 401)
		count(t, "recovery_codes", f.account.userID, 10)
	})
	t.Run("source attempt limit includes invalid offline codes", func(t *testing.T) {
		f := setup(t)
		f.p.verification.config.MaxSends = 2
		for i := 0; i < 3; i++ {
			status := 400
			if i == 2 {
				status = 429
			}
			requireStatus(t, request(f.mux, "POST", "/recovery/email", `{"email":"unknown@example.test","recoveryCode":"wrong"}`, nil), status)
		}
	})
	t.Run("account delivery cap survives changes in source budget", func(t *testing.T) {
		f := setup(t)
		f.p.verification.config.MaxSends = 2
		recoveryStart(t, f, f.codes[0])
		if _, err := db.Exec(`UPDATE recovery_delivery_limits SET last_sent_at=? WHERE account_id=?`, time.Now().UTC().Add(-time.Hour), f.account.userID); err != nil {
			t.Fatal(err)
		}
		recoveryStart(t, f, f.codes[0])
		if _, err := db.Exec(`UPDATE recovery_delivery_limits SET last_sent_at=? WHERE account_id=?`, time.Now().UTC().Add(-time.Hour), f.account.userID); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "/recovery/email", nil)
		r.RemoteAddr = net.JoinHostPort(net.IP(f.account.userID[:16]).String(), "1234")
		hash, err := f.p.verification.sourceHash(r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`DELETE FROM recovery_attempt_limits WHERE source_hash=?`, hash); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`DELETE FROM email_send_limits WHERE source_hash=?`, hash); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf(`{"email":%q,"recoveryCode":%q}`, f.account.email, f.codes[1])
		requireStatus(t, request(f.mux, "POST", "/recovery/email", body, nil), 429)
		if len(f.sender.codes) != 2 {
			t.Fatal("account send cap bypassed")
		}
	})
	t.Run("failed delivery preserves code and allows retry", func(t *testing.T) {
		f := setup(t)
		f.sender.fail = true
		body := fmt.Sprintf(`{"email":%q,"recoveryCode":%q}`, f.account.email, f.codes[0])
		requireStatus(t, request(f.mux, "POST", "/recovery/email", body, nil), 502)
		assertOriginalWorks(t, f)
		count(t, "recovery_requests", f.account.userID, 0)
		f.sender.fail = false
		recoveryStart(t, f, f.codes[0])
	})
	t.Run("failed persistence rolls back old credentials sessions and codes", func(t *testing.T) {
		f := setup(t)
		_, cookie := recoveryStart(t, f, f.codes[0])
		recoveryVerify(t, f, cookie)
		options := recoveryOptions(t, f, cookie)
		body, additional := registrationFixtureResponse(t, options, "http://localhost:5173", true)
		// Force a storage failure after deletion of old credentials, using a scoped
		// trigger only for this account. Integration tests run serially.
		trigger := "recovery_failure_test"
		if _, err := db.Exec(`CREATE TRIGGER ` + trigger + ` BEFORE INSERT ON passkey_credentials FOR EACH ROW BEGIN IF NEW.account_id=UNHEX('` + fmt.Sprintf("%x", f.account.userID) + `') THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='test storage failure'; END IF; END`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = db.Exec(`DROP TRIGGER IF EXISTS ` + trigger) })
		requireStatus(t, request(f.mux, "POST", "/recovery/passkey/finish", body, cookie), 503)
		assertOriginalWorks(t, f)
		hash := sha256.Sum256(additional.credentialID)
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM passkey_credentials WHERE id_hash=?`, hash[:]).Scan(&n); err != nil || n != 0 {
			t.Fatal("new credential survived failed transaction")
		}
	})
	t.Run("mutation endpoints reject cross origin and unauthenticated rotation", func(t *testing.T) {
		f := setup(t)
		for _, path := range []string{"/recovery/email", "/recovery/email/verify", "/recovery/passkey/options", "/recovery/passkey/finish", "/account/recovery-codes/options", "/account/recovery-codes/finish"} {
			r := httptest.NewRequest("POST", path, strings.NewReader("{}"))
			r.Header.Set("Origin", "https://evil.test")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			f.mux.ServeHTTP(w, r)
			requireStatus(t, w, 403)
		}
		requireStatus(t, request(f.mux, "POST", "/account/recovery-codes/options", "{}", nil), 401)
	})
}
