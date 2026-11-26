package main

import (
	"context"
	"database/sql"
	"time"
)

// Caller holds the stream lock and checks authorization/state. This shared path
// keeps creator termination and staff termination identical in resource cleanup.
func finishStream(ctx context.Context, tx *sql.Tx, id, actor, subject, report []byte, kind string, now time.Time) (int64, error) {
	rows, err := tx.QueryContext(ctx, "SELECT account_id FROM stream_participants WHERE stream_id=? AND left_at IS NULL", id)
	if err != nil {
		return 0, err
	}
	var departing [][]byte
	for rows.Next() {
		var account []byte
		if err = rows.Scan(&account); err != nil {
			break
		}
		departing = append(departing, account)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, account := range departing {
		if _, err = recordActivity(ctx, tx, "participant_left", actor, account, id, nil, report, now); err != nil {
			return 0, err
		}
	}
	_, err = tx.ExecContext(ctx, "UPDATE live_streams SET state='ended',ended_at=? WHERE id=?", now, id)
	if err == nil {
		_, err = tx.ExecContext(ctx, "UPDATE stream_publishing_grants SET revoked_at=COALESCE(revoked_at,?) WHERE stream_id=?", now, id)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, "UPDATE stream_participants SET left_at=?,publisher_namespace=NULL,publisher_seen_at=NULL,publishing_state='stopped' WHERE stream_id=? AND left_at IS NULL", now, id)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, "DELETE FROM stream_comment_publications WHERE stream_id=?", id)
	}
	if err == nil {
		err = hideStreamCommentContent(ctx, tx, id, now)
	}
	if err != nil {
		return 0, err
	}
	return recordActivity(ctx, tx, kind, actor, subject, id, nil, report, now)
}
