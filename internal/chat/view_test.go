package chat

import (
	"slices"
	"strings"
	"testing"
)

func TestProgressBar(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		ratio  float64
		wantFn func(t *testing.T, bar string)
	}{
		{
			name:  "empty when ratio negative",
			width: 8, ratio: -1,
			wantFn: func(t *testing.T, bar string) {
				if bar != "" {
					t.Fatalf("progressBar(8, -1) = %q, want empty", bar)
				}
			},
		},
		{
			name:  "zero ratio renders empty bar",
			width: 8, ratio: 0,
			wantFn: func(t *testing.T, bar string) {
				if bar == "" {
					t.Fatal("progressBar(8, 0) = empty, want empty bar (all ░)")
				}
				if strings.Contains(bar, "█") {
					t.Fatal("progressBar(8, 0) should be all empty (░), has filled blocks")
				}
			},
		},
		{
			name:  "empty when width too small",
			width: 1, ratio: 0.5,
			wantFn: func(t *testing.T, bar string) {
				if bar != "" {
					t.Fatalf("progressBar(1, 0.5) = %q, want empty", bar)
				}
			},
		},
		{
			name:  "has filled and empty blocks at 50%",
			width: 8, ratio: 0.5,
			wantFn: func(t *testing.T, bar string) {
				if bar == "" {
					t.Fatal("progressBar(8, 0.5) = empty, want bar")
				}
				if !strings.Contains(bar, "█") {
					t.Fatal("progressBar should contain filled blocks (█)")
				}
				if !strings.Contains(bar, "░") {
					t.Fatal("progressBar should contain empty blocks (░)")
				}
			},
		},
		{
			name:  "full at 100%",
			width: 8, ratio: 1.0,
			wantFn: func(t *testing.T, bar string) {
				if bar == "" {
					t.Fatal("progressBar(8, 1.0) = empty, want full bar")
				}
				if strings.Contains(bar, "░") {
					t.Fatal("progressBar(8, 1.0) should be fully filled, has empty blocks")
				}
			},
		},
		{
			name:  "clamps ratio above 1",
			width: 8, ratio: 2.0,
			wantFn: func(t *testing.T, bar string) {
				if bar == "" {
					t.Fatal("progressBar(8, 2.0) = empty, want clamped full bar")
				}
				if strings.Contains(bar, "░") {
					t.Fatal("progressBar(8, 2.0) should clamp to full, has empty blocks")
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.wantFn(t, progressBar(tc.width, tc.ratio))
		})
	}
}

func TestBudgetUsage(t *testing.T) {
	usd1 := 1.0
	usd050 := 0.50
	tests := []struct {
		name      string
		tokens    int
		usd       float64
		maxUSD    *float64
		maxTokens int
		wantPct   float64
		wantLabel string
	}{
		{name: "no budget", wantPct: 0, wantLabel: ""},
		{name: "usd budget", tokens: 0, usd: 0.25, maxUSD: &usd1, wantPct: 25, wantLabel: "$"},
		{name: "token budget", tokens: 300_000, maxTokens: 1_000_000, wantPct: 30, wantLabel: ""},
		{name: "usd precedes tokens", tokens: 999_999, usd: 0.10, maxUSD: &usd050, maxTokens: 1000, wantPct: 20, wantLabel: "$"},
		{name: "negative usd clamped", usd: -0.10, maxUSD: &usd1, wantPct: 0, wantLabel: "$"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pct, lbl := budgetUsage(tc.tokens, tc.usd, tc.maxUSD, tc.maxTokens)
			if pct != tc.wantPct || lbl != tc.wantLabel {
				t.Fatalf("budgetUsage(%d, %.2f, %v, %d) = %.0f %q, want %.0f %q",
					tc.tokens, tc.usd, tc.maxUSD, tc.maxTokens,
					pct, lbl, tc.wantPct, tc.wantLabel)
			}
		})
	}
}

// Line breaks and indentation of a multi-line message survive wrapping.
func TestWrapLinesKeepsLineBreaks(t *testing.T) {
	got := wrapLines("line1\nline2\n    indented", "  ", 40)
	want := []string{"line1", "  line2", "      indented"}
	if !slices.Equal(got, want) {
		t.Fatalf("wrapLines = %q, want %q", got, want)
	}
}
