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
	if !s.authenticateRelay(w, r) {
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
	if s.authorizeCommentPublisher(w, r, input.Token) {
		return
	}
	var stream, account, session []byte
	err = s.p.verification.db.QueryRowContext(r.Context(), `SELECT g.stream_id,g.account_id,g.session_id
 FROM stream_publishing_grants g
 JOIN live_streams s ON s.id=g.stream_id
 JOIN stream_participants p ON p.stream_id=g.stream_id AND p.account_id=g.account_id
 JOIN broadcaster_applications b ON b.account_id=g.account_id
 WHERE g.token_hash=? AND g.revoked_at IS NULL AND s.state='live'
 AND p.left_at IS NULL AND b.approved_at IS NOT NULL AND NOT EXISTS (SELECT 1 FROM account_broadcast_permissions bp WHERE bp.account_id=b.account_id AND bp.broadcasting_allowed=FALSE)`, tokenHash(input.Token)).Scan(&stream, &account, &session)
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

	tx, err := s.p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Publishing permission temporarily unavailable")
		return
	}
	defer tx.Rollback()
	if err = lockBroadcastAccounts(r.Context(), tx, account); err != nil {
		problem(w, 503, "Publishing permission temporarily unavailable")
		return
	}
	var state string
	if err = tx.QueryRowContext(r.Context(), "SELECT state FROM live_streams WHERE id=? FOR UPDATE", stream).Scan(&state); err != nil {
		problem(w, 503, "Publishing permission temporarily unavailable")
		return
	}
	now := s.p.verification.now()
	var authorizedSession []byte
	err = tx.QueryRowContext(r.Context(), `SELECT g.session_id FROM stream_publishing_grants g
 JOIN stream_participants p ON p.stream_id=g.stream_id AND p.account_id=g.account_id
 JOIN broadcaster_applications b ON b.account_id=g.account_id
 WHERE g.token_hash=? AND g.revoked_at IS NULL AND (g.last_relay_seen_at IS NULL OR g.last_relay_seen_at>?) AND p.left_at IS NULL AND b.approved_at IS NOT NULL AND NOT EXISTS (SELECT 1 FROM account_broadcast_permissions bp WHERE bp.account_id=b.account_id AND bp.broadcasting_allowed=FALSE) AND ?='live' FOR UPDATE`, tokenHash(input.Token), now.Add(-publisherLease), state).Scan(&authorizedSession)
	if err == sql.ErrNoRows {
		problem(w, 403, "Publishing permission denied")
		return
	}
	if err != nil {
		problem(w, 503, "Publishing permission temporarily unavailable")
		return
	}
	_, err = tx.ExecContext(r.Context(), "UPDATE stream_publishing_grants SET last_relay_seen_at=? WHERE token_hash=? AND revoked_at IS NULL", now, tokenHash(input.Token))
	if err != nil {
		problem(w, 503, "Publishing permission temporarily unavailable")
		return
	}
	_, err = tx.ExecContext(r.Context(), "UPDATE stream_participants SET publisher_seen_at=? WHERE stream_id=? AND account_id=? AND publisher_namespace=? AND publishing_state='publishing'", now, stream, account, namespace)
	if err != nil {
		problem(w, 503, "Publishing permission temporarily unavailable")
		return
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Publishing permission temporarily unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"namespace": namespace, "leaseSeconds": int(publisherLease.Seconds())})
}
