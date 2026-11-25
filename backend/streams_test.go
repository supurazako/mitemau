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
	s := &broadcasterServer{p: &passkeyServer{verification: v}, relayAuthKey: bytes.Repeat([]byte{0x71}, 32)}
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
	// A participant needs an invitation and current broadcasting permission.
	raw, _ := hex.DecodeString(id)
	if w := call("POST", "/streams/"+id+"/join", otherToken, "{}"); w.Code != 403 {
		t.Fatal("uninvited join", w.Code)
	}
	inviteBody := `{"accountId":"` + hex.EncodeToString(other) + `"}`
	if w := call("POST", "/streams/"+id+"/invite", otherToken, inviteBody); w.Code != 403 {
		t.Fatal("noncreator invited", w.Code)
	}
	for i := 0; i < 2; i++ {
		if w := call("POST", "/streams/"+id+"/invite", token, inviteBody); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := call("GET", "/account/invitations", otherToken, ""); !strings.Contains(w.Body.String(), id) {
		t.Fatal("invitation missing", w.Body.String())
	}
	if _, err = db.Exec("UPDATE broadcaster_applications SET approved_at=NULL WHERE account_id=?", other); err != nil {
		t.Fatal(err)
	}
	if w := call("POST", "/streams/"+id+"/join", otherToken, "{}"); w.Code != 403 {
		t.Fatal("unapproved join", w.Code)
	}
	if _, err = db.Exec("UPDATE broadcaster_applications SET approved_at=? WHERE account_id=?", now, other); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if w := call("POST", "/streams/"+id+"/join", otherToken, "{}"); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for i := 0; i < 2; i++ {
		if w := call("POST", "/streams/"+id+"/leave", otherToken, "{}"); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	publicationBody := `{"sessionId":"` + strings.Repeat("a1", 16) + `"}`
	if w := call("POST", "/streams/"+id+"/publishing", otherToken, publicationBody); w.Code != 403 {
		t.Fatal("departed participant published", w.Code)
	}
	var creatorActive int
	if err = db.QueryRow("SELECT COUNT(*) FROM stream_participants WHERE stream_id=? AND account_id=? AND left_at IS NULL", raw, owner).Scan(&creatorActive); err != nil || creatorActive != 1 {
		t.Fatal("leaving affected creator", err)
	}
	if w := call("POST", "/streams/"+id+"/join", otherToken, "{}"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("POST", "/streams/"+id+"/publishing", otherToken, publicationBody); w.Code != 409 {
		t.Fatal("unprepared session accepted", w.Code)
	}
	if w := call("POST", "/streams/"+id+"/publishing", otherToken, `{"sessionId":"`+strings.Repeat("a1", 16)+`","prepare":true}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("POST", "/streams/"+id+"/publishing", otherToken, publicationBody); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	relayCheck := func(credential string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/relay/authorize", strings.NewReader(`{"token":"`+credential+`"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+hex.EncodeToString(s.relayAuthKey))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	grantToken := s.publisherCredential("mitemau/streams/" + id + "/publishers/" + hex.EncodeToString(other) + "/" + strings.Repeat("a1", 16))
	if w := relayCheck(grantToken); w.Code != 200 {
		t.Fatal("authorized relay", w.Code, w.Body.String())
	}
	if _, err = db.Exec("UPDATE broadcaster_applications SET approved_at=NULL WHERE account_id=?", other); err != nil {
		t.Fatal(err)
	}
	if w := relayCheck(grantToken); w.Code != 403 {
		t.Fatal("unapproved grant accepted", w.Code)
	}
	if _, err = db.Exec("UPDATE broadcaster_applications SET approved_at=? WHERE account_id=?", now, other); err != nil {
		t.Fatal(err)
	}
	if w := relayCheck(strings.Repeat("00", 32)); w.Code != 403 {
		t.Fatal("unknown relay credential", w.Code)
	}
	unauthenticated := httptest.NewRecorder()
	mux.ServeHTTP(unauthenticated, httptest.NewRequest("POST", "/relay/authorize", strings.NewReader("{}")))
	if unauthenticated.Code != 401 {
		t.Fatal("relay service authentication", unauthenticated.Code)
	}
	if w := call("POST", "/streams/"+id+"/leave", otherToken, "{}"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := relayCheck(grantToken); w.Code != 403 {
		t.Fatal("departed grant accepted", w.Code)
	}
	if w := call("POST", "/streams/"+id+"/join", otherToken, "{}"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := relayCheck(grantToken); w.Code != 403 {
		t.Fatal("rejoin revived old grant", w.Code)
	}
	if w := call("POST", "/streams/"+id+"/publishing", otherToken, `{"sessionId":"`+strings.Repeat("a1", 16)+`","prepare":true}`); w.Code != 409 {
		t.Fatal("revoked prepare revived grant", w.Code)
	}
	// Use a fresh session after rejoining.
	publicationBody = `{"sessionId":"` + strings.Repeat("a4", 16) + `"}`
	if w := call("POST", "/streams/"+id+"/publishing", otherToken, `{"sessionId":"`+strings.Repeat("a4", 16)+`","prepare":true}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := call("POST", "/streams/"+id+"/publishing", otherToken, publicationBody); w.Code != 200 {
		t.Fatal(w.Code)
	}
	preparedBody := `{"sessionId":"` + strings.Repeat("a3", 16) + `","prepare":true}`
	prepared := call("POST", "/streams/"+id+"/publishing", otherToken, preparedBody)
	if prepared.Code != 200 {
		t.Fatal(prepared.Code, prepared.Body.String())
	}
	preparedRetry := call("POST", "/streams/"+id+"/publishing", otherToken, preparedBody)
	if preparedRetry.Code != 200 || preparedRetry.Body.String() != prepared.Body.String() {
		t.Fatal("prepare retry changed credential", preparedRetry.Code)
	}
	// A stop racing ahead of preparation must leave a tombstone.
	stoppedSession := strings.Repeat("a5", 16)
	if w := call("POST", "/streams/"+id+"/publishing", otherToken, `{"sessionId":"`+stoppedSession+`","stop":true}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := call("POST", "/streams/"+id+"/publishing", otherToken, `{"sessionId":"`+stoppedSession+`","prepare":true}`); w.Code != 409 {
		t.Fatal("late prepare revived stop", w.Code)
	}
	pendingParticipants := call("GET", "/streams/"+id+"/participants", token, "")
	if strings.Contains(pendingParticipants.Body.String(), strings.Repeat("a3", 16)) || !strings.Contains(pendingParticipants.Body.String(), strings.Repeat("a4", 16)) {
		t.Fatal("prepare published an unavailable track or replaced active publication", pendingParticipants.Body.String())
	}
	secondPublication := `{"sessionId":"` + strings.Repeat("a2", 16) + `"}`
	if w := call("POST", "/streams/"+id+"/publishing", otherToken, `{"sessionId":"`+strings.Repeat("a2", 16)+`","prepare":true}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	publicationResponse := call("POST", "/streams/"+id+"/publishing", otherToken, secondPublication)
	if publicationResponse.Code != 200 {
		t.Fatal(publicationResponse.Code, publicationResponse.Body.String())
	}
	if w := call("POST", "/streams/"+id+"/publishing", otherToken, `{"sessionId":"`+strings.Repeat("a1", 16)+`","stop":true}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := relayCheck(s.publisherCredential("mitemau/streams/" + id + "/publishers/" + hex.EncodeToString(other) + "/" + strings.Repeat("a2", 16))); w.Code != 200 {
		t.Fatal("old stop revoked new grant", w.Code)
	}
	participants := call("GET", "/streams/"+id+"/participants", token, "")
	if participants.Code != 200 || !strings.Contains(participants.Body.String(), strings.Repeat("a2", 16)) {
		t.Fatal("old stop removed new publication", participants.Body.String())
	}
	for i := 0; i < 2; i++ {
		if w := call("POST", "/streams/"+id+"/end", token, "{}"); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := relayCheck(s.publisherCredential("mitemau/streams/" + id + "/publishers/" + hex.EncodeToString(other) + "/" + strings.Repeat("a2", 16))); w.Code != 403 {
		t.Fatal("ended grant accepted", w.Code)
	}
	if w := call("POST", "/streams/"+id+"/publishing", otherToken, publicationBody); w.Code != 409 {
		t.Fatal("ended stream published", w.Code)
	}
	var active int
	if err = db.QueryRow("SELECT COUNT(*) FROM stream_participants WHERE stream_id=? AND left_at IS NULL", raw).Scan(&active); err != nil || active != 0 {
		t.Fatal("participants left active", err, active)
	}
	if w := call("POST", "/streams/"+id+"/start", token, "{}"); w.Code != 409 {
		t.Fatal("ended stream restarted", w.Code)
	}
	if w := call("POST", "/streams/"+id+"/join", otherToken, "{}"); w.Code != 409 {
		t.Fatal("joined ended stream", w.Code)
	}
	var list []liveStream
	w := call("GET", "/account/streams", otherToken, "")
	if err = json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].State != "live" {
		t.Fatal("independent stream changed", w.Body.String())
	}
}
