package main

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStaffConfiguration(t *testing.T) {
	id := strings.Repeat("ab", 32)
	for _, value := range []string{"", id, strings.ToUpper(id) + "," + id} {
		ids, err := parseStaffIDs(value)
		if err != nil {
			t.Fatal(err)
		}
		if value != "" && !ids[id] {
			t.Fatal("staff ID not normalized")
		}
	}
	for _, value := range []string{"email@example.com", "abcd", id + ","} {
		if _, err := parseStaffIDs(value); err == nil {
			t.Fatalf("accepted invalid setting %q", value)
		}
	}
}

func TestBroadcasterUnauthenticated(t *testing.T) {
	s := &broadcasterServer{}
	w := httptest.NewRecorder()
	if _, ok := s.authenticate(w, httptest.NewRequest("GET", "/", nil), true); ok || w.Code != 401 {
		t.Fatal("unauthenticated request allowed")
	}
}

func TestBroadcastersMySQL(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN not configured")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	staff := bytes.Repeat([]byte{0xd1}, 32)
	user := bytes.Repeat([]byte{0xd2}, 32)
	staffToken := strings.Repeat("c1", 32)
	userToken := strings.Repeat("c2", 32)
	for i, id := range [][]byte{staff, user} {
		token := []string{staffToken, userToken}[i]
		_, err = db.Exec("INSERT INTO accounts(id,email,created_at) VALUES (?,?,?)", id, hex.EncodeToString(id)+"@broadcaster.test", now)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Exec("DELETE FROM accounts WHERE id=?", id)
		defer db.Exec("DELETE FROM moderation_events WHERE account_id=?", id)
		_, err = db.Exec("INSERT INTO account_sessions(token_hash,account_id,expires_at,absolute_expires_at) VALUES (?,?,?,?)", tokenHash(token), id, now.Add(time.Hour), now.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
	}
	v := newVerificationServer(db, &recordingSender{}, testConfig())
	p := &passkeyServer{verification: v}
	s := &broadcasterServer{p: p, staffIDs: map[string]bool{hex.EncodeToString(staff): true}}
	mux := http.NewServeMux()
	s.routes(mux)
	request := func(method, path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", v.config.Origin)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := request("POST", "/account/broadcaster", userToken); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/staff/broadcasters", "/staff/broadcasters/" + hex.EncodeToString(user) + "/approve"} {
		method := "GET"
		if strings.HasSuffix(path, "/approve") {
			method = "POST"
		}
		if w := request(method, path, userToken); w.Code != 403 {
			t.Fatal("ordinary user allowed", w.Code)
		}
	}
	if w := request("POST", "/staff/broadcasters/"+hex.EncodeToString(user)+"/approve", staffToken); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("POST", "/account/broadcaster", userToken); !strings.Contains(w.Body.String(), "approved") {
		t.Fatal("reapplication reset approval", w.Body.String())
	}
	if w := request("GET", "/staff/broadcasters", staffToken); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal(w.Code, w.Body.String())
	}
	_, err = db.Exec("UPDATE account_sessions SET expires_at=? WHERE account_id=?", now.Add(-time.Hour), staff)
	if err != nil {
		t.Fatal(err)
	}
	if w := request("GET", "/staff/broadcasters", staffToken); w.Code != 401 {
		t.Fatal("expired staff allowed", w.Code)
	}
}
