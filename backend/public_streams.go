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

type publicStreamListing struct {
	publicStream
	WatchAccountID *string `json:"watchAccountId"`
}

func (s *broadcasterServer) publicStreamRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /public/streams", s.publicStreams)
	mux.HandleFunc("GET /public/streams/{id}", s.publicStream)
	mux.HandleFunc("GET /public/users/{id}", s.publicUser)
}

func (s *broadcasterServer) publicStreams(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	// A card must open a user page that currently resolves to this stream.
	// Keep the same current-stream ordering as publicUser below.
	rows, err := s.p.verification.db.QueryContext(r.Context(), `SELECT s.id,s.creator_id,s.title,(
 SELECT candidate.account_id FROM stream_participants candidate
 WHERE candidate.stream_id=s.id AND candidate.left_at IS NULL AND s.id=(
  SELECT live.id FROM stream_participants recent JOIN live_streams live ON live.id=recent.stream_id
  WHERE recent.account_id=candidate.account_id AND recent.left_at IS NULL AND live.state='live'
  ORDER BY recent.joined_at DESC,live.started_at DESC,live.id DESC LIMIT 1
 )
 ORDER BY (candidate.account_id=s.creator_id) DESC,candidate.joined_at,candidate.account_id LIMIT 1
 ) FROM live_streams s WHERE s.state='live' ORDER BY s.started_at DESC,s.id`)
	if err != nil {
		problem(w, 503, "Could not load live streams")
		return
	}
	defer rows.Close()
	streams := []publicStreamListing{}
	for rows.Next() {
		var id, creator, watchAccount []byte
		var item publicStreamListing
		if err = rows.Scan(&id, &creator, &item.Title, &watchAccount); err != nil {
			problem(w, 503, "Could not load live streams")
			return
		}
		item.ID = hex.EncodeToString(id)
		item.CreatorID = hex.EncodeToString(creator)
		if watchAccount != nil {
			encoded := hex.EncodeToString(watchAccount)
			item.WatchAccountID = &encoded
		}
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
