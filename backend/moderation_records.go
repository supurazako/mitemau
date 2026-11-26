package main

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"time"
)

const (
	commentRetention  = 30 * 24 * time.Hour
	activityRetention = 90 * 24 * time.Hour
	evidenceRetention = 180 * 24 * time.Hour
)

// All fields are explicit. Never pass arbitrary request bodies, tokens or media here.
func recordActivity(ctx context.Context, tx *sql.Tx, kind string, actor, account, stream, comment, report []byte, now time.Time) (int64, error) {
	retention := activityRetention
	switch kind {
	case "stream_started", "stream_ended", "participant_joined", "participant_left", "comment_posted", "comment_deleted":
	case "report_created", "report_resolved", "staff_comment_deleted", "posting_suspended", "posting_restored", "broadcasting_suspended", "broadcasting_restored", "stream_force_ended", "broadcaster_approved":
		retention = evidenceRetention
	default:
		return 0, fmt.Errorf("unsupported activity kind")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO moderation_events(kind,actor_id,account_id,stream_id,comment_id,report_id,occurred_at,expires_at) VALUES (?,?,?,?,?,?,?,?)`, kind, actor, account, stream, comment, report, now, now.Add(retention))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// #52 calls this inside the same transaction as the accepted comment.
func recordCommentPosted(ctx context.Context, tx *sql.Tx, account, stream, comment []byte, body string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO moderation_comment_content(comment_id,account_id,stream_id,body,posted_at) VALUES (?,?,?,?,?)`, comment, account, stream, body, now); err != nil {
		return err
	}
	_, err := recordActivity(ctx, tx, "comment_posted", account, account, stream, comment, nil, now)
	return err
}

// #53 calls this when removing a comment. Retrying cannot restart the clock.
func hideCommentContent(ctx context.Context, tx *sql.Tx, comment []byte, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE moderation_comment_content SET hidden_at=?,expires_at=? WHERE comment_id=? AND hidden_at IS NULL`, now, now.Add(commentRetention), comment)
	return err
}

func hideStreamCommentContent(ctx context.Context, tx *sql.Tx, stream []byte, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE moderation_comment_content SET hidden_at=?,expires_at=? WHERE stream_id=? AND hidden_at IS NULL`, now, now.Add(commentRetention), stream)
	return err
}

// #54 locks its report row first and supplies that report's original acceptance
// time. A retry cannot renew a snapshot. Expired source data cannot be recovered.
func retainReportEvidence(ctx context.Context, tx *sql.Tx, report []byte, eventID int64, acceptedAt, now time.Time) error {
	if acceptedAt.After(now) || !acceptedAt.Add(evidenceRetention).After(now) {
		return fmt.Errorf("report evidence retention expired or invalid")
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO moderation_evidence(report_id,event_id,source_key,kind,actor_id,account_id,stream_id,comment_id,occurred_at,body,accepted_at,expires_at)
 SELECT ?,e.id,?,e.kind,e.actor_id,e.account_id,e.stream_id,e.comment_id,e.occurred_at,c.body,?,?
 FROM moderation_events e LEFT JOIN moderation_comment_content c ON c.comment_id=e.comment_id AND (c.expires_at IS NULL OR c.expires_at>?)
 WHERE e.id=? AND e.expires_at>? AND e.occurred_at<=?
 ON DUPLICATE KEY UPDATE event_id=event_id`, report, "event:"+strconv.FormatInt(eventID, 10), acceptedAt, acceptedAt.Add(evidenceRetention), now, eventID, now, now)
	if err != nil {
		return err
	}
	return nil
}

// A live comment can outlast its 90-day posting event. Reports may snapshot its
// still-retained content directly, without recreating an expired activity event.
func retainCommentEvidence(ctx context.Context, tx *sql.Tx, report, comment []byte, acceptedAt, now time.Time) error {
	if acceptedAt.After(now) || !acceptedAt.Add(evidenceRetention).After(now) {
		return fmt.Errorf("report evidence retention expired or invalid")
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO moderation_evidence(report_id,event_id,source_key,kind,actor_id,account_id,stream_id,comment_id,occurred_at,body,accepted_at,expires_at)
 SELECT ?,NULL,?,'comment_posted',account_id,account_id,stream_id,comment_id,posted_at,body,?,?
 FROM moderation_comment_content WHERE comment_id=? AND (expires_at IS NULL OR expires_at>?) AND posted_at<=?
 ON DUPLICATE KEY UPDATE source_key=source_key`, report, "comment:"+hex.EncodeToString(comment), acceptedAt, acceptedAt.Add(evidenceRetention), comment, now, now)
	return err
}

func (s *broadcasterServer) cleanupModeration(ctx context.Context) error {
	now := s.p.verification.now()
	for _, table := range []string{"moderation_evidence", "moderation_comment_content", "moderation_events"} {
		if _, err := s.p.verification.db.ExecContext(ctx, "DELETE FROM "+table+" WHERE expires_at<=? LIMIT 1000", now); err != nil {
			return err
		}
	}
	return nil
}

func (s *broadcasterServer) runModerationCleanup(ctx context.Context) {
	run := func() {
		work, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := s.cleanupModeration(work); err != nil {
			log.Print("moderation cleanup failed; check database migrations and connectivity")
		}
	}
	run()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
