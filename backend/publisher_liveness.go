package main

import (
	"context"
	"log"
	"time"
)

const publisherLease = 15 * time.Second

// Project expiry on reads too, so a delayed cleanup cannot advertise a dead session.
const visiblePublishing = `CASE WHEN p.publishing_state='publishing' AND p.publisher_seen_at>? THEN p.publisher_namespace ELSE NULL END,
 CASE WHEN p.publishing_state='publishing' AND (p.publisher_seen_at IS NULL OR p.publisher_seen_at<=?) THEN 'disconnected' ELSE p.publishing_state END`

func (s *broadcasterServer) cleanupPublishers(ctx context.Context) error {
	now := s.p.verification.now()
	cutoff := now.Add(-publisherLease)
	// Separate conditional writes avoid locking all grants and participants together.
	if _, err := s.p.verification.db.ExecContext(ctx, `UPDATE stream_publishing_grants SET revoked_at=? WHERE revoked_at IS NULL AND last_relay_seen_at<=?`, now, cutoff); err != nil {
		return err
	}
	_, err := s.p.verification.db.ExecContext(ctx, `UPDATE stream_participants SET publisher_namespace=NULL,publisher_seen_at=NULL,publishing_state='disconnected' WHERE publishing_state='publishing' AND (publisher_seen_at IS NULL OR publisher_seen_at<=?)`, cutoff)
	return err
}

func (s *broadcasterServer) runPublisherCleanup(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		cleanupContext, cancel := context.WithTimeout(ctx, 3*time.Second)
		if err := s.cleanupPublishers(cleanupContext); err != nil {
			log.Printf("publisher cleanup: %v", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
