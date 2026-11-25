package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"net/http"
)

// Deriving the credential makes retries of prepare return the same grant.
// A revoked session ID is never reactivated, including after rejoining.
func (s *broadcasterServer) publisherCredential(namespace string) string {
	mac := hmac.New(sha256.New, s.relayAuthKey)
	mac.Write([]byte("mitemau/publishing/v1/" + namespace))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *broadcasterServer) authorizeRelay(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	expected := "Bearer " + hex.EncodeToString(s.relayAuthKey)
	if len(s.relayAuthKey) != 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(expected)) != 1 {
		problem(w, 401, "Relay authentication required")
		return
	}
	var input struct {
		Token string `json:"token"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	raw, err := hex.DecodeString(input.Token)
	if err != nil || len(raw) != 32 {
		problem(w, 403, "Publishing permission denied")
		return
	}
	var stream, account, session []byte
	err = s.p.verification.db.QueryRowContext(r.Context(), `SELECT g.stream_id,g.account_id,g.session_id
 FROM stream_publishing_grants g
 JOIN live_streams s ON s.id=g.stream_id
 JOIN stream_participants p ON p.stream_id=g.stream_id AND p.account_id=g.account_id
 JOIN broadcaster_applications b ON b.account_id=g.account_id
 WHERE g.token_hash=? AND g.revoked_at IS NULL AND s.state='live'
 AND p.left_at IS NULL AND b.approved_at IS NOT NULL`, tokenHash(input.Token)).Scan(&stream, &account, &session)
	if err == sql.ErrNoRows {
		problem(w, 403, "Publishing permission denied")
		return
	}
	if err != nil {
		problem(w, 503, "Publishing permission temporarily unavailable")
		return
	}
	namespace := "mitemau/streams/" + hex.EncodeToString(stream) + "/publishers/" + hex.EncodeToString(account) + "/" + hex.EncodeToString(session)
	if subtle.ConstantTimeCompare([]byte(input.Token), []byte(s.publisherCredential(namespace))) != 1 {
		problem(w, 403, "Publishing permission denied")
		return
	}
	writeJSON(w, 200, map[string]any{"namespace": namespace, "leaseSeconds": 15})
}
