package stream

import (
	"github.com/acestream/acestream/internal/config"
	"time"
)

// Called only by the reader-session owner. There is no asynchronous recovery
// queue: stopping a reader and negotiating its replacement cannot enqueue a
// signal for the next generation.
func stallDue(now, sessionStarted, lastChunk, nextAllowed time.Time, clients int, cfg *config.Config) bool {
	if cfg.StreamStallTimeout <= 0 || clients == 0 || now.Before(nextAllowed) {
		return false
	}
	threshold := cfg.StreamStallTimeout
	if lastChunk.IsZero() {
		lastChunk = sessionStarted
		threshold = max(threshold, cfg.ChannelInitGracePeriod)
	}
	return now.Sub(lastChunk) >= threshold
}
