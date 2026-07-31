package analysis

import (
	"context"
	"errors"
	"testing"
)

func TestGuardEnforcesDepthNodesAndCancellation(t *testing.T) {
	limits, err := (Limits{MaxDepth: 1, MaxNodesPerRoot: 2, MaxTotalNodes: 3}).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	guard := NewGuard(context.Background(), limits)
	guard.BeginRoot()
	if err := guard.Visit(0); err != nil {
		t.Fatal(err)
	}
	if err := guard.Visit(2); err == nil {
		t.Fatal("expected depth budget error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewGuard(ctx, limits).Visit(0); !errors.Is(err, context.Canceled) {
		t.Fatalf("Visit cancellation = %v", err)
	}
}
