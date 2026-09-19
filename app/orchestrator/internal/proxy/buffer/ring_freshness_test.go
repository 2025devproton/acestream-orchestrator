package buffer

import (
	"testing"
	"time"
)

func TestCompleteChunkFreshnessAndReset(t *testing.T) {
	r := New(188*2, 4)
	r.Write(make([]byte, 188))
	if !r.LastChunkWriteTime().IsZero() || r.IsChunkFresh(time.Hour) {
		t.Fatal("partial write marked chunk progress")
	}
	r.Write(make([]byte, 188))
	if r.LastChunkWriteTime().IsZero() || !r.IsChunkFresh(time.Hour) {
		t.Fatal("completed chunk not recorded")
	}
	r.Reset()
	if !r.LastChunkWriteTime().IsZero() || r.IsChunkFresh(time.Hour) {
		t.Fatal("reset retained previous generation timestamp")
	}
}
