package iikoplugins

import (
	"context"
	"errors"
	"testing"
)

func TestRapidScanPercentClampsRange(t *testing.T) {
	tests := []struct {
		name      string
		processed int
		total     int
		want      int
	}{
		{name: "zero total", processed: 1, total: 0, want: 0},
		{name: "mid range", processed: 5, total: 20, want: 25},
		{name: "complete", processed: 20, total: 20, want: 100},
		{name: "overflow", processed: 25, total: 20, want: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rapidScanPercent(tt.processed, tt.total); got != tt.want {
				t.Fatalf("rapidScanPercent(%d, %d) = %d, want %d", tt.processed, tt.total, got, tt.want)
			}
		})
	}
}

func TestScanPluginZipFilesWithProgressHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := scanPluginZipFilesWithProgress(ctx, RapidPluginsBaseURL, maxScanDepth, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
