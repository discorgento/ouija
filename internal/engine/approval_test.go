package engine

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestApprovalManagerSessionCache(t *testing.T) {
	am := NewApprovalManager()
	ctx := context.Background()

	// No callback set → deny by default.
	result, err := am.Request(ctx, "edit", nil)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if result.Allowed {
		t.Fatal("expected denied (no callback)")
	}

	// Set a callback that always approves.
	am.SetCallback(func(ctx context.Context, tool string, _ map[string]any) (ApprovalResult, error) {
		return ApprovalResult{Allowed: true, ForSession: false}, nil
	})

	// First call goes through callback.
	result, err = am.Request(ctx, "edit", nil)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if !result.Allowed {
		t.Fatal("expected allowed")
	}
	if result.ForSession {
		t.Fatal("expected not for session")
	}

	// Second call also goes through callback (not cached).
	result, err = am.Request(ctx, "edit", nil)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if !result.Allowed {
		t.Fatal("expected allowed")
	}
}

func TestApprovalManagerForSession(t *testing.T) {
	am := NewApprovalManager()
	ctx := context.Background()

	callCount := 0
	am.SetCallback(func(ctx context.Context, tool string, _ map[string]any) (ApprovalResult, error) {
		callCount++
		return ApprovalResult{Allowed: true, ForSession: true}, nil
	})

	// First call → callback invoked.
	result, err := am.Request(ctx, "grep", nil)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if !result.Allowed {
		t.Fatal("expected allowed")
	}
	if !result.ForSession {
		t.Fatal("expected for session")
	}
	if callCount != 1 {
		t.Fatalf("expected 1 callback call, got %d", callCount)
	}

	// Second call → cached, callback NOT invoked.
	result, err = am.Request(ctx, "grep", nil)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if !result.Allowed {
		t.Fatal("expected allowed")
	}
	if callCount != 1 {
		t.Fatalf("expected still 1 callback call, got %d (session cache miss)", callCount)
	}

	// Different tool → callback invoked again.
	result, err = am.Request(ctx, "edit", nil)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if !result.Allowed {
		t.Fatal("expected allowed")
	}
	if callCount != 2 {
		t.Fatalf("expected 2 callback calls, got %d", callCount)
	}
}

func TestApprovalManagerTimeBasedSession(t *testing.T) {
	am := NewApprovalManager()
	ctx := context.Background()

	callCount := 0
	am.SetCallback(func(ctx context.Context, tool string, _ map[string]any) (ApprovalResult, error) {
		callCount++
		return ApprovalResult{Allowed: true, ForSession: true, Duration: 50 * time.Millisecond}, nil
	})

	// First call → callback invoked, cached for 50ms.
	result, err := am.Request(ctx, "grep", nil)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if !result.Allowed {
		t.Fatal("expected allowed")
	}
	if callCount != 1 {
		t.Fatalf("expected 1 callback call, got %d", callCount)
	}

	// Second call within window → cached, no callback.
	result, err = am.Request(ctx, "grep", nil)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if !result.Allowed {
		t.Fatal("expected allowed (cached)")
	}
	if callCount != 1 {
		t.Fatalf("expected still 1 callback call, got %d", callCount)
	}

	// Wait for expiry.
	time.Sleep(60 * time.Millisecond)

	// After expiry → callback invoked again.
	result, err = am.Request(ctx, "grep", nil)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if !result.Allowed {
		t.Fatal("expected allowed")
	}
	if callCount != 2 {
		t.Fatalf("expected 2 callback calls after expiry, got %d", callCount)
	}
}

func TestApprovalManagerReset(t *testing.T) {
	am := NewApprovalManager()
	ctx := context.Background()

	callCount := 0
	am.SetCallback(func(ctx context.Context, tool string, _ map[string]any) (ApprovalResult, error) {
		callCount++
		return ApprovalResult{Allowed: true, ForSession: true}, nil
	})

	// Approve for session.
	am.Request(ctx, "edit", nil)

	// Reset clears the cache.
	am.Reset()

	// Callback invoked again after reset.
	am.Request(ctx, "edit", nil)
	if callCount != 2 {
		t.Fatalf("expected 2 callback calls after reset, got %d", callCount)
	}
}

func TestApprovalManagerContextCancel(t *testing.T) {
	am := NewApprovalManager()

	ctx, cancel := context.WithCancel(context.Background())

	am.SetCallback(func(ctx context.Context, tool string, _ map[string]any) (ApprovalResult, error) {
		cancel()
		<-ctx.Done()
		return ApprovalResult{}, ctx.Err()
	})

	result, err := am.Request(ctx, "edit", nil)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if result.Allowed {
		t.Fatal("expected not allowed after cancel")
	}
}

func TestApprovalManagerConcurrent(t *testing.T) {
	am := NewApprovalManager()
	ctx := context.Background()

	var mu sync.Mutex
	callCount := 0

	am.SetCallback(func(ctx context.Context, tool string, _ map[string]any) (ApprovalResult, error) {
		mu.Lock()
		callCount++
		mu.Unlock()
		return ApprovalResult{Allowed: true, ForSession: false}, nil
	})

	// Fire 10 concurrent requests for the same tool.
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := am.Request(ctx, "edit", nil)
			if err != nil {
				t.Errorf("Request: %v", err)
			}
		}()
	}
	wg.Wait()

	// Each call is independent (not cached since ForSession=false).
	mu.Lock()
	if callCount != 10 {
		t.Fatalf("expected 10 callback calls, got %d", callCount)
	}
	mu.Unlock()
}
