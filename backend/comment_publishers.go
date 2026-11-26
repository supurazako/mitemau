package main

import (
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"
)

func commentNamespace(stream, session []byte) string {
	return "mitemau/streams/" + hex.EncodeToString(stream) + "/comments/" + hex.EncodeToString(session)
}
func (s *broadcasterServer) authenticateRelay(w http.ResponseWriter, r *http.Request) bool {
	expected := "Bearer " + hex.EncodeToString(s.relayAuthKey)
	if len(s.relayAuthKey) != 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(expected)) != 1 {
		problem(w, 401, "Relay authentication required")
		return false
	}
	return true
}
func (s *broadcasterServer) commentPublisherRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /internal/comment-streams", s.commentStreams)
	mux.HandleFunc("GET /internal/comments/{id}", s.internalComments)
	mux.HandleFunc("POST /internal/comments/{id}/prepare", s.prepareCommentPublisher)
	mux.HandleFunc("POST /internal/comments/{id}/activate", s.activateCommentPublisher)
	mux.HandleFunc("POST /relay/comments/read", s.authorizeCommentRead)
}
func (s *broadcasterServer) commentStreams(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.authenticateRelay(w, r) {
		return
	}
	rows, err := s.p.verification.db.QueryContext(r.Context(), "SELECT id FROM live_streams WHERE state='live' ORDER BY id")
	if err != nil {
		problem(w, 503, "Could not load streams")
		return
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id []byte
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, hex.EncodeToString(id))
	}
	if err != nil || rows.Err() != nil {
		problem(w, 503, "Could not load streams")
		return
	}
	writeJSON(w, 200, ids)
}
func (s *broadcasterServer) prepareCommentPublisher(w http.ResponseWriter, r *http.Request) {
	s.updateCommentPublisher(w, r, false)
}
func (s *broadcasterServer) activateCommentPublisher(w http.ResponseWriter, r *http.Request) {
	s.updateCommentPublisher(w, r, true)
}
func (s *broadcasterServer) updateCommentPublisher(w http.ResponseWriter, r *http.Request, activate bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.authenticateRelay(w, r) {
		return
	}
	stream, err := streamID(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "Invalid stream ID")
		return
	}
	var input struct {
		SessionID string `json:"sessionId"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	session, err := streamID(input.SessionID)
	if err != nil {
		problem(w, 400, "Invalid session ID")
		return
	}
	namespace := commentNamespace(stream, session)
	credential := s.publisherCredential(namespace)
	tx, err := s.p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Could not update comment publisher")
		return
	}
	defer tx.Rollback()
	var state string
	err = tx.QueryRowContext(r.Context(), "SELECT state FROM live_streams WHERE id=? FOR UPDATE", stream).Scan(&state)
	if err == sql.ErrNoRows {
		problem(w, 404, "Stream not found")
		return
	}
	if err != nil {
		problem(w, 503, "Could not update comment publisher")
		return
	}
	if state != "live" {
		problem(w, 409, "Stream is not live")
		return
	}
	if activate {
		var permitted int
		err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM stream_comment_publications WHERE stream_id=? AND session_id=? AND token_hash=? AND last_relay_seen_at>?", stream, session, tokenHash(credential), s.p.verification.now().Add(-publisherLease)).Scan(&permitted)
		if err == nil && permitted == 0 {
			problem(w, 409, "Connect a fresh comment publisher")
			return
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), "UPDATE stream_comment_publications SET ready=TRUE WHERE stream_id=? AND session_id=?", stream, session)
		}
	} else {
		// One active sending session per stream. A new epoch retires the old grant.
		_, err = tx.ExecContext(r.Context(), `INSERT INTO stream_comment_publications(stream_id,session_id,token_hash) VALUES (?,?,?)
 ON DUPLICATE KEY UPDATE ready=IF(session_id=VALUES(session_id),ready,FALSE),last_relay_seen_at=IF(session_id=VALUES(session_id),last_relay_seen_at,NULL),token_hash=VALUES(token_hash),session_id=VALUES(session_id)`, stream, session, tokenHash(credential))
	}
	if err != nil {
		problem(w, 503, "Could not update comment publisher")
		return
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Could not update comment publisher")
		return
	}
	result := map[string]string{"namespace": namespace}
	if !activate {
		result["token"] = credential
	}
	writeJSON(w, 200, result)
}

// Return false only when this is not a comment-publisher credential.
func (s *broadcasterServer) authorizeCommentPublisher(w http.ResponseWriter, r *http.Request, credential string) bool {
	var stream, session []byte
	err := s.p.verification.db.QueryRowContext(r.Context(), "SELECT stream_id,session_id FROM stream_comment_publications WHERE token_hash=?", tokenHash(credential)).Scan(&stream, &session)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		problem(w, 503, "Publishing permission temporarily unavailable")
		return true
	}
	namespace := commentNamespace(stream, session)
	if subtle.ConstantTimeCompare([]byte(credential), []byte(s.publisherCredential(namespace))) != 1 {
		problem(w, 403, "Publishing permission denied")
		return true
	}
	tx, err := s.p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Publishing permission temporarily unavailable")
		return true
	}
	defer tx.Rollback()
	var state string
	err = tx.QueryRowContext(r.Context(), "SELECT state FROM live_streams WHERE id=? FOR UPDATE", stream).Scan(&state)
	if err != nil {
		problem(w, 503, "Publishing permission temporarily unavailable")
		return true
	}
	var seen sql.NullTime
	err = tx.QueryRowContext(r.Context(), "SELECT last_relay_seen_at FROM stream_comment_publications WHERE stream_id=? AND session_id=? AND token_hash=? FOR UPDATE", stream, session, tokenHash(credential)).Scan(&seen)
	now := s.p.verification.now()
	if err == sql.ErrNoRows || (err == nil && (state != "live" || (seen.Valid && !seen.Time.After(now.Add(-publisherLease))))) {
		problem(w, 403, "Publishing permission denied")
		return true
	}
	if err != nil {
		problem(w, 503, "Publishing permission temporarily unavailable")
		return true
	}
	_, err = tx.ExecContext(r.Context(), "UPDATE stream_comment_publications SET last_relay_seen_at=? WHERE stream_id=? AND session_id=?", now, stream, session)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		problem(w, 503, "Publishing permission temporarily unavailable")
		return true
	}
	writeJSON(w, 200, map[string]any{"namespace": namespace, "leaseSeconds": int(publisherLease.Seconds())})
	return true
}
func (s *broadcasterServer) authorizeCommentRead(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.authenticateRelay(w, r) {
		return
	}
	var input struct {
		Namespace string `json:"namespace"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	parts := strings.Split(input.Namespace, "/")
	if len(parts) != 5 || parts[0] != "mitemau" || parts[1] != "streams" || parts[3] != "comments" {
		problem(w, 403, "Comment channel unavailable")
		return
	}
	stream, err := streamID(parts[2])
	if err != nil {
		problem(w, 403, "Comment channel unavailable")
		return
	}
	session, err := streamID(parts[4])
	if err != nil {
		problem(w, 403, "Comment channel unavailable")
		return
	}
	var permitted int
	err = s.p.verification.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM stream_comment_publications p JOIN live_streams s ON s.id=p.stream_id WHERE p.stream_id=? AND p.session_id=? AND p.ready=TRUE AND p.last_relay_seen_at>? AND s.state='live'`, stream, session, s.p.verification.now().Add(-publisherLease)).Scan(&permitted)
	if err != nil {
		problem(w, 503, "Comment channel temporarily unavailable")
		return
	}
	if permitted == 0 {
		problem(w, 403, "Comment channel unavailable")
		return
	}
	writeJSON(w, 200, map[string]bool{"allowed": true})
}
