package main

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStreamsMySQL(t *testing.T) {
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
	owner := bytes.Repeat([]byte{0xe1}, 32)
	other := bytes.Repeat([]byte{0xe2}, 32)
	for i, id := range [][]byte{owner, other} {
		if _, err = db.Exec("INSERT INTO accounts(id,email,created_at) VALUES (?,?,?)", id, hex.EncodeToString(id)+"@stream.test", now); err != nil {
			t.Fatal(err)
		}
		defer db.Exec("DELETE FROM accounts WHERE id=?", id)
		token := strings.Repeat([]string{"e1", "e2"}[i], 32)
		if _, err = db.Exec("INSERT INTO account_sessions(token_hash,account_id,expires_at,absolute_expires_at) VALUES (?,?,?,?)", tokenHash(token), id, now.Add(time.Hour), now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	// Remove streams first because they retain creator and participant references.
	defer func() { db.Exec("DELETE FROM live_streams WHERE creator_id IN (?,?)", owner, other) }()
	v := newVerificationServer(db, &recordingSender{}, testConfig())
	s := &broadcasterServer{p: &passkeyServer{verification: v}}
	mux := http.NewServeMux()
	s.routes(mux)
	call := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", v.config.Origin)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	token := strings.Repeat("e1", 32)
	otherToken := strings.Repeat("e2", 32)
	id := strings.Repeat("f1", 16)
	body := `{"id":"` + id + `","title":"Work chat"}`
	if w := call("POST", "/streams", token, body); w.Code != 403 {
		t.Fatal("unapproved creation", w.Code)
	}
	for _, account := range [][]byte{owner, other} {
		if _, err = db.Exec("INSERT INTO broadcaster_applications(account_id,requested_at,approved_at) VALUES (?,?,?)", account, now, now); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if w := call("POST", "/streams", token, body); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := call("POST", "/streams/"+id+"/end", otherToken, "{}"); w.Code != 403 {
		t.Fatal("noncreator ended stream", w.Code)
	}
	for i := 0; i < 2; i++ {
		if w := call("POST", "/streams/"+id+"/start", token, "{}"); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	secondID := strings.Repeat("f2", 16)
	if w := call("POST", "/streams", otherToken, `{"id":"`+secondID+`","title":"Other chat"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("POST", "/streams/"+secondID+"/start", otherToken, "{}"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// A second participant must be closed by ending the shared stream.
	raw, _ := hex.DecodeString(id)
	if _, err = db.Exec("INSERT INTO stream_participants(stream_id,account_id,joined_at) VALUES (?,?,?)", raw, other, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if w := call("POST", "/streams/"+id+"/end", token, "{}"); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var active int
	if err = db.QueryRow("SELECT COUNT(*) FROM stream_participants WHERE stream_id=? AND left_at IS NULL", raw).Scan(&active); err != nil || active != 0 {
		t.Fatal("participants left active", err, active)
	}
	if w := call("POST", "/streams/"+id+"/start", token, "{}"); w.Code != 409 {
		t.Fatal("ended stream restarted", w.Code)
	}
	var list []liveStream
	w := call("GET", "/account/streams", otherToken, "")
	if err = json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].State != "live" {
		t.Fatal("independent stream changed", w.Body.String())
	}
}
