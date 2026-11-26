package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const commentLimit = 50
const commentLength = 500
const commentInterval = 2 * time.Second

type liveComment struct {
	ID        string    `json:"id"`
	Sequence  string    `json:"sequence"`
	AccountID string    `json:"accountId"`
	Body      string    `json:"body"`
	PostedAt  time.Time `json:"postedAt"`
}
type commentSnapshot struct {
	StreamID  string        `json:"streamId"`
	Version   string        `json:"version"`
	Namespace *string       `json:"namespace"`
	Comments  []liveComment `json:"comments"`
}

func (s *broadcasterServer) commentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /public/streams/{id}/comments", s.publicComments)
	mux.HandleFunc("GET /public/streams/{id}/comments/channel", s.commentChannel)
	mux.HandleFunc("POST /streams/{id}/comments", s.p.verification.protect(s.postComment))
	mux.HandleFunc("GET /streams/{id}/comments/permissions", s.commentPermissions)
	mux.HandleFunc("DELETE /streams/{id}/comments/{comment}", s.p.verification.protect(s.deleteComment))
	s.commentPublisherRoutes(mux)
}
func (s *broadcasterServer) publicComments(w http.ResponseWriter, r *http.Request) {
	s.commentFeed(w, r, false, false)
}
func (s *broadcasterServer) commentChannel(w http.ResponseWriter, r *http.Request) {
	s.commentFeed(w, r, false, true)
}
func (s *broadcasterServer) internalComments(w http.ResponseWriter, r *http.Request) {
	s.commentFeed(w, r, true, false)
}
func (s *broadcasterServer) commentFeed(w http.ResponseWriter, r *http.Request, internal, channel bool) {
	w.Header().Set("Cache-Control", "no-store")
	if internal && !s.authenticateRelay(w, r) {
		return
	}
	id, err := streamID(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "Invalid stream ID")
		return
	}
	tx, err := s.p.verification.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		problem(w, 503, "Could not load comments")
		return
	}
	defer tx.Rollback()
	var state string
	var version uint64
	err = tx.QueryRowContext(r.Context(), "SELECT state,comment_version FROM live_streams WHERE id=?", id).Scan(&state, &version)
	if err == sql.ErrNoRows || (err == nil && state != "live") {
		problem(w, 404, "Stream is unavailable or has ended")
		return
	}
	if err != nil {
		problem(w, 503, "Could not load comments")
		return
	}
	snapshot := commentSnapshot{StreamID: hex.EncodeToString(id), Version: strconv.FormatUint(version, 10), Comments: []liveComment{}}
	var session []byte
	err = tx.QueryRowContext(r.Context(), "SELECT session_id FROM stream_comment_publications WHERE stream_id=? AND ready=TRUE AND last_relay_seen_at>?", id, s.p.verification.now().Add(-publisherLease)).Scan(&session)
	if err != nil && err != sql.ErrNoRows {
		problem(w, 503, "Could not load comment channel")
		return
	}
	if session != nil {
		namespace := commentNamespace(id, session)
		snapshot.Namespace = &namespace
	}
	if !channel {
		rows, err := tx.QueryContext(r.Context(), `SELECT c.comment_id,c.sequence,c.account_id,b.body,c.posted_at FROM stream_comments c JOIN moderation_comment_content b ON b.comment_id=c.comment_id WHERE c.stream_id=? AND b.hidden_at IS NULL ORDER BY c.sequence DESC LIMIT ?`, id, commentLimit)
		if err != nil {
			problem(w, 503, "Could not load comments")
			return
		}
		for rows.Next() {
			var item liveComment
			var comment, account []byte
			var sequence uint64
			if err = rows.Scan(&comment, &sequence, &account, &item.Body, &item.PostedAt); err != nil {
				break
			}
			item.ID = hex.EncodeToString(comment)
			item.Sequence = strconv.FormatUint(sequence, 10)
			item.AccountID = hex.EncodeToString(account)
			snapshot.Comments = append(snapshot.Comments, item)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			problem(w, 503, "Could not load comments")
			return
		}
		for a, b := 0, len(snapshot.Comments)-1; a < b; a, b = a+1, b-1 {
			snapshot.Comments[a], snapshot.Comments[b] = snapshot.Comments[b], snapshot.Comments[a]
		}
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Could not load comments")
		return
	}
	writeJSON(w, 200, snapshot)
}
func (s *broadcasterServer) postComment(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	stream, err := streamID(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "Invalid stream ID")
		return
	}
	var input struct {
		RequestID string `json:"requestId"`
		Body      string `json:"body"`
	}
	if !decodeBodyLimit(w, r, &input, 8192) {
		return
	}
	requestID, err := streamID(input.RequestID)
	body := strings.TrimSpace(input.Body)
	if err != nil || !utf8.ValidString(body) || body == "" || utf8.RuneCountInString(body) > commentLength {
		problem(w, 400, "Provide a comment of 1–500 characters and a valid request ID")
		return
	}
	tx, err := s.p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Could not post comment")
		return
	}
	defer tx.Rollback()
	var locked []byte
	if err = tx.QueryRowContext(r.Context(), "SELECT id FROM accounts WHERE id=? FOR UPDATE", actor).Scan(&locked); err != nil {
		problem(w, 503, "Could not check comment permission")
		return
	}
	var allowed bool = true
	var suspensionReason sql.NullString
	err = tx.QueryRowContext(r.Context(), "SELECT posting_allowed,suspension_reason FROM account_comment_permissions WHERE account_id=? FOR UPDATE", actor).Scan(&allowed, &suspensionReason)
	if err != nil && err != sql.ErrNoRows {
		problem(w, 503, "Could not check comment permission")
		return
	}
	if !allowed {
		message := "Comment posting is suspended"
		if suspensionReason.Valid {
			message += ": " + suspensionReason.String
		}
		problem(w, 403, message)
		return
	}
	var state string
	err = tx.QueryRowContext(r.Context(), "SELECT state FROM live_streams WHERE id=? FOR UPDATE", stream).Scan(&state)
	if err == sql.ErrNoRows {
		problem(w, 404, "Stream not found")
		return
	}
	if err != nil {
		problem(w, 503, "Could not post comment")
		return
	}
	if state != "live" {
		problem(w, 409, "Stream is not live")
		return
	}
	var previousStream, previousActor, previousComment []byte
	var previousBody sql.NullString
	err = tx.QueryRowContext(r.Context(), `SELECT c.stream_id,c.account_id,c.comment_id,b.body FROM stream_comments c LEFT JOIN moderation_comment_content b ON b.comment_id=c.comment_id AND b.hidden_at IS NULL WHERE c.account_id=? AND c.request_id=?`, actor, requestID).Scan(&previousStream, &previousActor, &previousComment, &previousBody)
	if err == nil {
		if hex.EncodeToString(previousStream) != hex.EncodeToString(stream) || hex.EncodeToString(previousActor) != hex.EncodeToString(actor) || !previousBody.Valid || previousBody.String != body {
			problem(w, 409, "Comment request ID already used")
			return
		}
		writeJSON(w, 200, map[string]string{"id": hex.EncodeToString(previousComment)})
		return
	}
	if err != sql.ErrNoRows {
		problem(w, 503, "Could not post comment")
		return
	}
	now := s.p.verification.now()
	var next time.Time
	err = tx.QueryRowContext(r.Context(), "SELECT next_post_at FROM comment_posting_cooldowns WHERE account_id=?", actor).Scan(&next)
	if err != nil && err != sql.ErrNoRows {
		problem(w, 503, "Could not check posting rate")
		return
	}
	if now.Before(next) {
		w.Header().Set("Retry-After", "2")
		problem(w, 429, "Wait two seconds between comments")
		return
	}
	comment := make([]byte, 16)
	if _, err = rand.Read(comment); err != nil {
		problem(w, 503, "Could not create comment ID")
		return
	}
	_, err = tx.ExecContext(r.Context(), "INSERT INTO stream_comments(comment_id,request_id,stream_id,account_id,posted_at) VALUES (?,?,?,?,?)", comment, requestID, stream, actor, now)
	if err == nil {
		err = recordCommentPosted(r.Context(), tx, actor, stream, comment, body, now)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), "INSERT INTO comment_posting_cooldowns(account_id,next_post_at) VALUES (?,?) ON DUPLICATE KEY UPDATE next_post_at=VALUES(next_post_at)", actor, now.Add(commentInterval))
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), "UPDATE live_streams SET comment_version=comment_version+1 WHERE id=?", stream)
	}
	if err != nil {
		problem(w, 503, "Could not post comment")
		return
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Could not post comment")
		return
	}
	writeJSON(w, 200, map[string]string{"id": hex.EncodeToString(comment)})
}
