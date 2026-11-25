package main

import (
	"database/sql"
	"encoding/hex"
	"net/http"
)

type publicStream struct {
	ID        string `json:"id"`
	CreatorID string `json:"creatorId"`
	Title     string `json:"title"`
}

func (s *broadcasterServer) publicStreamRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /public/streams", s.publicStreams)
	mux.HandleFunc("GET /public/streams/{id}", s.publicStream)
	mux.HandleFunc("GET /public/users/{id}", s.publicUser)
}

func (s *broadcasterServer) publicStreams(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	rows, err := s.p.verification.db.QueryContext(r.Context(), "SELECT id,creator_id,title FROM live_streams WHERE state='live' ORDER BY started_at DESC,id")
	if err != nil {
		problem(w, 503, "Could not load live streams")
		return
	}
	defer rows.Close()
	streams := []publicStream{}
	for rows.Next() {
		var id, creator []byte
		var item publicStream
		if err = rows.Scan(&id, &creator, &item.Title); err != nil {
			problem(w, 503, "Could not load live streams")
			return
		}
		item.ID = hex.EncodeToString(id)
		item.CreatorID = hex.EncodeToString(creator)
		streams = append(streams, item)
	}
	if rows.Err() != nil {
		problem(w, 503, "Could not load live streams")
		return
	}
	writeJSON(w, 200, streams)
}

func (s *broadcasterServer) publicStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, err := streamID(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "Invalid stream ID")
		return
	}
	// One statement keeps stream state and the visible participants in one snapshot.
	rows, err := s.p.verification.db.QueryContext(r.Context(), `SELECT s.id,s.creator_id,s.title,p.account_id,p.publisher_namespace
 FROM live_streams s LEFT JOIN stream_participants p ON p.stream_id=s.id AND p.left_at IS NULL
 WHERE s.id=? AND s.state='live' ORDER BY p.joined_at,p.account_id`, id)
	if err != nil {
		problem(w, 503, "Could not load stream")
		return
	}
	defer rows.Close()
	var item publicStream
	participants := []streamParticipant{}
	for rows.Next() {
		var raw, creator, account []byte
		var namespace sql.NullString
		if err = rows.Scan(&raw, &creator, &item.Title, &account, &namespace); err != nil {
			problem(w, 503, "Could not load stream")
			return
		}
		item.ID = hex.EncodeToString(raw)
		item.CreatorID = hex.EncodeToString(creator)
		if account != nil {
			participant := streamParticipant{AccountID: hex.EncodeToString(account)}
			if namespace.Valid {
				participant.Namespace = &namespace.String
			}
			participants = append(participants, participant)
		}
	}
	if rows.Err() != nil {
		problem(w, 503, "Could not load stream")
		return
	}
	if item.ID == "" {
		problem(w, 404, "Stream is unavailable or has ended")
		return
	}
	writeJSON(w, 200, map[string]any{"stream": item, "participants": participants})
}

func (s *broadcasterServer) publicUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	account, err := hex.DecodeString(r.PathValue("id"))
	if err != nil || len(account) != 32 {
		problem(w, 400, "Invalid account ID")
		return
	}
	// Prefer the most recently joined live stream if older data has overlapping participation.
	var raw, creator []byte
	var title sql.NullString
	var rawAccount []byte
	err = s.p.verification.db.QueryRowContext(r.Context(), `SELECT a.id,s.id,s.creator_id,s.title
 FROM accounts a LEFT JOIN live_streams s ON s.id=(
  SELECT live.id FROM stream_participants p JOIN live_streams live ON live.id=p.stream_id
  WHERE p.account_id=a.id AND p.left_at IS NULL AND live.state='live'
  ORDER BY p.joined_at DESC,live.started_at DESC,live.id DESC LIMIT 1
 ) WHERE a.id=?`, account).Scan(&rawAccount, &raw, &creator, &title)
	if err == sql.ErrNoRows {
		problem(w, 404, "User not found")
		return
	}
	if err != nil {
		problem(w, 503, "Could not load user stream")
		return
	}
	var item *publicStream
	if raw != nil {
		item = &publicStream{ID: hex.EncodeToString(raw), CreatorID: hex.EncodeToString(creator), Title: title.String}
	}
	writeJSON(w, 200, map[string]any{"accountId": hex.EncodeToString(rawAccount), "stream": item})
}
