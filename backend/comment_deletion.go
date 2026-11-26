package main

import (
	"database/sql"
	"net/http"
)

const commentDeletionPermission = `SELECT COUNT(*) FROM stream_participants p JOIN broadcaster_applications a ON a.account_id=p.account_id JOIN live_streams s ON s.id=p.stream_id WHERE p.stream_id=? AND p.account_id=? AND p.left_at IS NULL AND a.approved_at IS NOT NULL AND s.state='live'`

func (s *broadcasterServer) commentPermissions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	id, err := streamID(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "Invalid stream ID")
		return
	}
	var count int
	if err = s.p.verification.db.QueryRowContext(r.Context(), commentDeletionPermission, id, actor).Scan(&count); err != nil {
		problem(w, 503, "Could not check comment permission")
		return
	}
	writeJSON(w, 200, map[string]bool{"canDelete": count > 0})
}

func (s *broadcasterServer) deleteComment(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	id, err := streamID(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "Invalid stream ID")
		return
	}
	comment, err := streamID(r.PathValue("comment"))
	if err != nil {
		problem(w, 400, "Invalid comment ID")
		return
	}
	tx, err := s.p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Could not delete comment")
		return
	}
	defer tx.Rollback()
	// Participation changes, stream end, posts and deletion all lock the stream.
	// A leave/end that commits first therefore removes deletion permission.
	var state string
	err = tx.QueryRowContext(r.Context(), "SELECT state FROM live_streams WHERE id=? FOR UPDATE", id).Scan(&state)
	if err == sql.ErrNoRows {
		problem(w, 404, "Stream not found")
		return
	}
	if err != nil {
		problem(w, 503, "Could not delete comment")
		return
	}
	if state != "live" {
		problem(w, 409, "Stream is not live")
		return
	}
	var permitted int
	if err = tx.QueryRowContext(r.Context(), commentDeletionPermission, id, actor).Scan(&permitted); err != nil {
		problem(w, 503, "Could not check comment permission")
		return
	}
	if permitted == 0 {
		problem(w, 403, "Active broadcaster participation required")
		return
	}
	var author []byte
	var hidden sql.NullTime
	err = tx.QueryRowContext(r.Context(), `SELECT c.account_id,b.hidden_at FROM stream_comments c LEFT JOIN moderation_comment_content b ON b.comment_id=c.comment_id WHERE c.stream_id=? AND c.comment_id=? FOR UPDATE`, id, comment).Scan(&author, &hidden)
	if err == sql.ErrNoRows {
		problem(w, 404, "Comment not found")
		return
	}
	if err != nil {
		problem(w, 503, "Could not delete comment")
		return
	}
	// The content may already have expired; metadata still identifies the comment.
	var retained int
	if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM moderation_comment_content WHERE comment_id=?", comment).Scan(&retained); err != nil {
		problem(w, 503, "Could not delete comment")
		return
	}
	if !hidden.Valid && retained > 0 {
		now := s.p.verification.now()
		err = hideCommentContent(r.Context(), tx, comment, now)
		if err == nil {
			_, err = recordActivity(r.Context(), tx, "comment_deleted", actor, author, id, comment, nil, now)
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), "UPDATE live_streams SET comment_version=comment_version+1 WHERE id=?", id)
		}
		if err != nil {
			problem(w, 503, "Could not delete comment")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Could not delete comment")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
