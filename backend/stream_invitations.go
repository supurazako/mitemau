package main

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"net/http"
)

func (s *broadcasterServer) invitationRoutes(mux *http.ServeMux) {
	s.publisherRoutes(mux)
	mux.HandleFunc("GET /account/invitations", s.invitations)
	mux.HandleFunc("POST /streams/{id}/invite", s.p.verification.protect(s.invite))
	mux.HandleFunc("POST /streams/{id}/join", s.p.verification.protect(s.join))
	mux.HandleFunc("POST /streams/{id}/leave", s.p.verification.protect(s.leave))
}
func (s *broadcasterServer) invitations(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	rows, err := s.p.verification.db.QueryContext(r.Context(), `SELECT s.id,s.creator_id,s.title,s.state
 FROM stream_invitations i JOIN live_streams s ON s.id=i.stream_id
 LEFT JOIN stream_participants p ON p.stream_id=i.stream_id AND p.account_id=i.account_id
 WHERE i.account_id=? AND s.state<>'ended' AND (p.account_id IS NULL OR p.left_at IS NOT NULL)
 ORDER BY i.invited_at DESC`, actor)
	if err != nil {
		problem(w, 503, "Could not load invitations")
		return
	}
	defer rows.Close()
	items := []liveStream{}
	for rows.Next() {
		var id, creator []byte
		var item liveStream
		if err = rows.Scan(&id, &creator, &item.Title, &item.State); err != nil {
			problem(w, 503, "Could not load invitations")
			return
		}
		item.ID = hex.EncodeToString(id)
		item.CreatorID = hex.EncodeToString(creator)
		items = append(items, item)
	}
	if rows.Err() != nil {
		problem(w, 503, "Could not load invitations")
		return
	}
	writeJSON(w, 200, items)
}

// Lock the stream before invitation/participation updates, matching whole-stream
// termination, so joining cannot leave an active participant in an ended stream.
func (s *broadcasterServer) participation(w http.ResponseWriter, r *http.Request, operation string) {
	actor, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	id, err := streamID(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "Invalid stream ID")
		return
	}
	var input struct {
		AccountID string `json:"accountId"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	tx, err := s.p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Could not update participation")
		return
	}
	defer tx.Rollback()
	var creator []byte
	var state string
	err = tx.QueryRowContext(r.Context(), "SELECT creator_id,state FROM live_streams WHERE id=? FOR UPDATE", id).Scan(&creator, &state)
	if err == sql.ErrNoRows {
		problem(w, 404, "Stream not found")
		return
	}
	if err != nil {
		problem(w, 503, "Could not update participation")
		return
	}
	target := actor
	if operation == "invite" {
		if !bytes.Equal(creator, actor) {
			problem(w, 403, "Only the creator can invite")
			return
		}
		target, err = hex.DecodeString(input.AccountID)
		if err != nil || len(target) != 32 {
			problem(w, 400, "Invalid account ID")
			return
		}
		if bytes.Equal(target, actor) {
			problem(w, 409, "The creator already participates")
			return
		}
	}
	if state == "ended" && operation != "leave" {
		problem(w, 409, "This stream has ended")
		return
	}
	now := s.p.verification.now()
	switch operation {
	case "invite", "join":
		var approved int
		if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM broadcaster_applications WHERE account_id=? AND approved_at IS NOT NULL", target).Scan(&approved); err != nil {
			problem(w, 503, "Could not check permission")
			return
		}
		if approved == 0 {
			problem(w, 403, "Broadcasting permission required")
			return
		}
		if operation == "invite" {
			_, err = tx.ExecContext(r.Context(), "INSERT INTO stream_invitations(stream_id,account_id,invited_at) VALUES (?,?,?) ON DUPLICATE KEY UPDATE account_id=VALUES(account_id)", id, target, now)
		} else {
			var invited int
			if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM stream_invitations WHERE stream_id=? AND account_id=?", id, actor).Scan(&invited); err != nil {
				problem(w, 503, "Could not check invitation")
				return
			}
			if invited == 0 {
				problem(w, 403, "Invitation required")
				return
			}
			_, err = tx.ExecContext(r.Context(), `INSERT INTO stream_participants(stream_id,account_id,joined_at,left_at) VALUES (?,?,?,NULL)
    ON DUPLICATE KEY UPDATE joined_at=IF(left_at IS NULL,joined_at,VALUES(joined_at)),left_at=NULL`, id, actor, now)
		}
	case "leave":
		_, err = tx.ExecContext(r.Context(), "UPDATE stream_publishing_grants SET revoked_at=COALESCE(revoked_at,?) WHERE stream_id=? AND account_id=?", now, id, actor)
		if err != nil {
			problem(w, 503, "Could not revoke publishing")
			return
		}
		_, err = tx.ExecContext(r.Context(), "UPDATE stream_participants SET left_at=?,publisher_namespace=NULL,publisher_seen_at=NULL,publishing_state='stopped' WHERE stream_id=? AND account_id=? AND left_at IS NULL", now, id, actor)
	}
	if err != nil {
		problem(w, 503, "Could not update participation")
		return
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Could not update participation")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *broadcasterServer) invite(w http.ResponseWriter, r *http.Request) {
	s.participation(w, r, "invite")
}
func (s *broadcasterServer) join(w http.ResponseWriter, r *http.Request) {
	s.participation(w, r, "join")
}
func (s *broadcasterServer) leave(w http.ResponseWriter, r *http.Request) {
	s.participation(w, r, "leave")
}
