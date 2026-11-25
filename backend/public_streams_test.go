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

func TestPublicStreamsMySQL(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN not configured")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	owner := bytes.Repeat([]byte{0xd1}, 32)
	guest := bytes.Repeat([]byte{0xd2}, 32)
	offline := bytes.Repeat([]byte{0xd3}, 32)
	now := time.Now().UTC()
	for _, account := range [][]byte{owner, guest, offline} {
		if _, err = db.Exec("INSERT INTO accounts(id,email,created_at) VALUES(?,?,?)", account, hex.EncodeToString(account)+"@public.test", now); err != nil {
			t.Fatal(err)
		}
		defer db.Exec("DELETE FROM accounts WHERE id=?", account)
	}
	first := bytes.Repeat([]byte{0xc1}, 16)
	second := bytes.Repeat([]byte{0xc2}, 16)
	ready := bytes.Repeat([]byte{0xc3}, 16)
	defer db.Exec("DELETE FROM live_streams WHERE creator_id IN (?,?)", owner, guest)
	for index, id := range [][]byte{first, second, ready} {
		creator := owner
		if index == 1 {
			creator = guest
		}
		state := "live"
		if index == 2 {
			state = "ready"
		}
		if _, err = db.Exec("INSERT INTO live_streams(id,creator_id,title,state,created_at,started_at) VALUES(?,?,?,?,?,?)", id, creator, []string{"Shared live", "Other live", "Private preparation"}[index], state, now, now); err != nil {
			t.Fatal(err)
		}
	}
	firstNamespace := "mitemau/streams/" + hex.EncodeToString(first) + "/publishers/" + hex.EncodeToString(owner) + "/session"
	otherNamespace := "mitemau/streams/" + hex.EncodeToString(second) + "/publishers/" + hex.EncodeToString(guest) + "/session"
	for index, account := range [][]byte{owner, guest} {
		if _, err = db.Exec("INSERT INTO stream_participants(stream_id,account_id,joined_at,publisher_namespace) VALUES(?,?,?,?)", first, account, now.Add(time.Duration(index)*time.Second), "mitemau/streams/"+hex.EncodeToString(first)+"/publishers/"+hex.EncodeToString(account)+"/session"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec("INSERT INTO stream_participants(stream_id,account_id,joined_at,publisher_namespace) VALUES(?,?,?,?)", second, guest, now.Add(-time.Minute), otherNamespace); err != nil {
		t.Fatal(err)
	}
	server := &broadcasterServer{p: &passkeyServer{verification: newVerificationServer(db, &recordingSender{}, testConfig())}}
	mux := http.NewServeMux()
	server.routes(mux)
	get := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("GET", path, nil)
		// Public reads must work independently of a stale or invalid site session.
		request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "invalid-session"})
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		return response
	}
	checkUser := func(account []byte, expected string) {
		t.Helper()
		response := get("/public/users/" + hex.EncodeToString(account))
		var result struct {
			AccountID string        `json:"accountId"`
			Stream    *publicStream `json:"stream"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatal("user page", response.Code, response.Body.String())
		}
		if result.AccountID != hex.EncodeToString(account) {
			t.Fatal("wrong public account")
		}
		if expected == "" {
			if result.Stream != nil {
				t.Fatal("offline account exposed a stream")
			}
		} else if result.Stream == nil || result.Stream.ID != expected {
			t.Fatal("wrong shared stream", response.Body.String())
		}
		if strings.Contains(response.Body.String(), "@public.test") {
			t.Fatal("public account exposed email")
		}
	}
	list := get("/public/streams")
	var streams []publicStream
	if list.Code != 200 || json.Unmarshal(list.Body.Bytes(), &streams) != nil || len(streams) != 2 {
		t.Fatal("live list", list.Code, list.Body.String())
	}
	if strings.Contains(list.Body.String(), "Private preparation") || strings.Contains(list.Body.String(), "@public.test") {
		t.Fatal("private data in live list")
	}
	if list.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("live state was cacheable")
	}
	for _, account := range [][]byte{owner, guest} {
		checkUser(account, hex.EncodeToString(first))
	}
	checkUser(offline, "")
	detail := get("/public/streams/" + hex.EncodeToString(first))
	var result struct {
		Stream       publicStream        `json:"stream"`
		Participants []streamParticipant `json:"participants"`
	}
	if detail.Code != 200 || json.Unmarshal(detail.Body.Bytes(), &result) != nil || len(result.Participants) != 2 {
		t.Fatal("public participants", detail.Code, detail.Body.String())
	}
	if result.Stream.ID != hex.EncodeToString(first) || !strings.Contains(detail.Body.String(), firstNamespace) {
		t.Fatal("wrong stream media", detail.Body.String())
	}
	other := get("/public/streams/" + hex.EncodeToString(second))
	if other.Code != 200 || strings.Contains(other.Body.String(), firstNamespace) || !strings.Contains(other.Body.String(), otherNamespace) {
		t.Fatal("cross-stream media", other.Body.String())
	}
	for path, status := range map[string]int{
		"/public/streams/" + hex.EncodeToString(ready):            404,
		"/public/streams/" + strings.Repeat("f0", 16):             404,
		"/public/users/" + strings.Repeat("f0", 32):               404,
		"/public/streams/bad":                                     400,
		"/public/users/bad":                                       400,
		"/streams/" + hex.EncodeToString(first) + "/participants": 401,
	} {
		if response := get(path); response.Code != status {
			t.Fatalf("%s returned %d", path, response.Code)
		}
	}
	if _, err = db.Exec("UPDATE live_streams SET state='ended' WHERE id=?", first); err != nil {
		t.Fatal(err)
	}
	if response := get("/public/streams/" + hex.EncodeToString(first)); response.Code != 404 {
		t.Fatal("ended stream remained public")
	}
	checkUser(owner, "")
	checkUser(guest, hex.EncodeToString(second))
	list = get("/public/streams")
	if json.Unmarshal(list.Body.Bytes(), &streams) != nil || len(streams) != 1 || streams[0].ID != hex.EncodeToString(second) {
		t.Fatal("ended stream remained listed", list.Body.String())
	}
	if _, err = db.Exec("UPDATE live_streams SET state='ended' WHERE id=?", second); err != nil {
		t.Fatal(err)
	}
	if response := get("/public/streams"); response.Code != 200 || strings.TrimSpace(response.Body.String()) != "[]" {
		t.Fatal("empty live list", response.Body.String())
	}
}
