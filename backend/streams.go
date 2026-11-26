package main

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"
)

type liveStream struct {
	ID          string  `json:"id"`
	CreatorID   string  `json:"creatorId"`
	Title       string  `json:"title"`
	State       string  `json:"state"`
	PublisherID string  `json:"publisherId"`
	EndReason   *string `json:"endReason,omitempty"`
}

func (s *broadcasterServer) streamRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /account/streams", s.ownedStreams)
	s.publicStreamRoutes(mux)
	s.invitationRoutes(mux)
	mux.HandleFunc("POST /streams", s.p.verification.protect(s.createStream))
	mux.HandleFunc("POST /streams/{id}/start", s.p.verification.protect(s.startStream))
	mux.HandleFunc("POST /streams/{id}/end", s.p.verification.protect(s.endStream))
}

func (s *broadcasterServer) ownedStreams(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	rows, err := s.p.verification.db.QueryContext(r.Context(), `SELECT s.id,s.creator_id,s.title,s.state,n.reason FROM live_streams s
 LEFT JOIN stream_termination_notices n ON n.stream_id=s.id AND n.expires_at>?
 LEFT JOIN stream_participants p ON p.stream_id=s.id AND p.account_id=?
 WHERE s.creator_id=? OR (p.account_id IS NOT NULL AND (p.left_at IS NULL OR n.stream_id IS NOT NULL))
 ORDER BY s.created_at DESC,s.id`, s.p.verification.now(), id, id)
	if err != nil {
		problem(w, 503, "Could not load streams")
		return
	}
	defer rows.Close()
	streams := []liveStream{}
	for rows.Next() {
		var raw, creator []byte
		var item liveStream
		var reason sql.NullString
		if err = rows.Scan(&raw, &creator, &item.Title, &item.State, &reason); err != nil {
			problem(w, 503, "Could not load streams")
			return
		}
		if reason.Valid {
			item.EndReason = &reason.String
		}
		item.ID = hex.EncodeToString(raw)
		item.CreatorID = hex.EncodeToString(creator)
		item.PublisherID = hex.EncodeToString(id)
		streams = append(streams, item)
	}
	if rows.Err() != nil {
		problem(w, 503, "Could not load streams")
		return
	}
	writeJSON(w, 200, streams)
}

func streamID(value string) ([]byte, error) {
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != 16 {
		return nil, errors.New("invalid stream ID")
	}
	return raw, nil
}

func (s *broadcasterServer) createStream(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	var input struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	id, err := streamID(input.ID)
	title := strings.TrimSpace(input.Title)
	if err != nil || title == "" || utf8.RuneCountInString(title) > 200 {
		problem(w, 400, "Provide a stream ID and a title of 1–200 characters")
		return
	}
	tx, err := s.p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Could not create stream")
		return
	}
	defer tx.Rollback()
	// Lock the creator before checking permission and creating a stream.
	var locked []byte
	if err = tx.QueryRowContext(r.Context(), "SELECT id FROM accounts WHERE id=? FOR UPDATE", actor).Scan(&locked); err != nil {
		problem(w, 503, "Could not create stream")
		return
	}
	var approved int
	if err = tx.QueryRowContext(r.Context(), approvedBroadcastSQL, actor).Scan(&approved); err != nil {
		problem(w, 503, "Could not check permission")
		return
	}
	if approved == 0 {
		problem(w, 403, "Broadcasting permission required")
		return
	}
	var creator []byte
	var item liveStream
	err = tx.QueryRowContext(r.Context(), "SELECT creator_id,title,state FROM live_streams WHERE id=? FOR UPDATE", id).Scan(&creator, &item.Title, &item.State)
	if err == nil {
		if !bytes.Equal(creator, actor) || item.Title != title {
			problem(w, 409, "Stream ID already used")
			return
		}
	} else if err == sql.ErrNoRows {
		now := s.p.verification.now()
		if _, err = tx.ExecContext(r.Context(), "INSERT INTO live_streams(id,creator_id,title,state,created_at) VALUES (?,?,?,'ready',?)", id, actor, title, now); err != nil {
			problem(w, 503, "Could not create stream")
			return
		}
		if _, err = tx.ExecContext(r.Context(), "INSERT INTO stream_participants(stream_id,account_id,joined_at) VALUES (?,?,?)", id, actor, now); err != nil {
			problem(w, 503, "Could not create stream")
			return
		}
		if _, err = recordActivity(r.Context(), tx, "participant_joined", actor, actor, id, nil, nil, now); err != nil {
			problem(w, 503, "Could not record participation")
			return
		}
		item.Title = title
		item.State = "ready"
	} else {
		problem(w, 503, "Could not create stream")
		return
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Could not create stream")
		return
	}
	item.ID = hex.EncodeToString(id)
	item.CreatorID = hex.EncodeToString(actor)
	item.PublisherID = hex.EncodeToString(actor)
	writeJSON(w, 200, item)
}
func (s *broadcasterServer) startStream(w http.ResponseWriter, r *http.Request) {
	s.transitionStream(w, r, "live")
}
func (s *broadcasterServer) endStream(w http.ResponseWriter, r *http.Request) {
	s.transitionStream(w, r, "ended")
}

func (s *broadcasterServer) transitionStream(w http.ResponseWriter, r *http.Request, target string) {
	actor, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	id, err := streamID(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "Invalid stream ID")
		return
	}
	var input struct{}
	if !decodeBody(w, r, &input) {
		return
	}
	tx, err := s.p.verification.db.BeginTx(r.Context(), nil)
	if err != nil {
		problem(w, 503, "Could not update stream")
		return
	}
	defer tx.Rollback()
	if target == "live" {
		if err = lockBroadcastAccounts(r.Context(), tx, actor); err != nil {
			problem(w, 503, "Could not check broadcasting permission")
			return
		}
	}
	var creator []byte
	var item liveStream
	err = tx.QueryRowContext(r.Context(), "SELECT creator_id,title,state FROM live_streams WHERE id=? FOR UPDATE", id).Scan(&creator, &item.Title, &item.State)
	if err == sql.ErrNoRows {
		problem(w, 404, "Stream not found")
		return
	}
	if err != nil {
		problem(w, 503, "Could not update stream")
		return
	}
	if !bytes.Equal(creator, actor) {
		problem(w, 403, "Only the creator can start or end this stream")
		return
	}
	if target == "live" {
		var approved int
		if err = tx.QueryRowContext(r.Context(), approvedBroadcastSQL, actor).Scan(&approved); err != nil {
			problem(w, 503, "Could not check permission")
			return
		}
		if approved == 0 {
			problem(w, 403, "Broadcasting permission required")
			return
		}
		if item.State == "ended" {
			problem(w, 409, "This stream has ended")
			return
		}
	}
	if item.State != target {
		now := s.p.verification.now()
		if target == "live" {
			_, err = tx.ExecContext(r.Context(), "UPDATE live_streams SET state='live',started_at=? WHERE id=?", now, id)
		} else {
			_, err = finishStream(r.Context(), tx, id, actor, creator, nil, "stream_ended", now)
		}
		if err == nil && target == "live" {
			_, err = recordActivity(r.Context(), tx, "stream_started", actor, creator, id, nil, nil, now)
		}

		if err != nil {
			problem(w, 503, "Could not update stream")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		problem(w, 503, "Could not update stream")
		return
	}
	item.ID = hex.EncodeToString(id)
	item.CreatorID = hex.EncodeToString(actor)
	item.PublisherID = hex.EncodeToString(actor)
	item.State = target
	writeJSON(w, 200, item)
}
