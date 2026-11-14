package campaign

import (
	"testing"
	"time"
)

func TestBucketWidth(t *testing.T) {
	t.Parallel()
	tests := []struct {
		span time.Duration
		want int
	}{
		{0, 1},
		{5 * time.Second, 1},
		{599 * time.Second, 1}, // up to 600 one-second buckets
		{600 * time.Second, 2},
		{20 * time.Minute, 5}, // 240 five-second buckets
		{time.Hour, 10},
		{24 * time.Hour, 300},
		{10 * 365 * 24 * time.Hour, 86400}, // the widest there is, however long
	}
	for _, tt := range tests {
		if got := bucketWidth(tt.span); got != tt.want {
			t.Errorf("bucketWidth(%v) = %d, want %d", tt.span, got, tt.want)
		}
		if n := int(tt.span/(time.Duration(bucketWidth(tt.span))*time.Second)) + 1; n > MaxTimelineBuckets+1 && tt.want != 86400 {
			t.Errorf("a span of %v makes %d buckets, want at most %d", tt.span, n, MaxTimelineBuckets)
		}
	}
}
