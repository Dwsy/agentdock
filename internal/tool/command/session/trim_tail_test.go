package session

import (
	"fmt"
	"strings"
	"testing"
)

func TestTrimTailKeepsLastLines(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&sb, "line-%d\n", i)
	}
	// Use the default byte budget so the line cap is active.
	got := trimTail(sb.String(), DefaultOutputBytes, 3)
	if !got.Spilled {
		t.Fatal("trimTail: spilled = false, want true when lines were dropped")
	}
	want := "line-7\nline-8\nline-9\n"
	if got.Text != want {
		t.Fatalf("trimTail text = %q, want %q (tail, not head)", got.Text, want)
	}
}

func TestTrimTailKeepsByteTail(t *testing.T) {
	value := "0123456789abcdefghij"
	got := trimTail(value, 5, DefaultOutputLines)
	if !got.Spilled || got.Text != "fghij" {
		t.Fatalf("trimTail = %q spilled=%v, want tail 5 bytes", got.Text, got.Spilled)
	}
}

func TestTrimTailSingleLongLineKeepsEnd(t *testing.T) {
	value := strings.Repeat("abcdefghij", 20) // 200 bytes, one line
	got := trimTail(value, 50, DefaultOutputLines)
	if !got.Spilled || len(got.Text) != 50 {
		t.Fatalf("trimTail len=%d spilled=%v, want 50 bytes", len(got.Text), got.Spilled)
	}
	if got.Text != value[len(value)-50:] {
		t.Fatal("trimTail: single long line must keep its END, not its start")
	}
}

func TestTrimTailNoOpWhenUnderLimits(t *testing.T) {
	value := "a\nb\nc\n"
	got := trimTail(value, 1<<20, DefaultOutputLines)
	if got.Spilled || got.Text != value {
		t.Fatalf("trimTail = %q spilled=%v, want unchanged", got.Text, got.Spilled)
	}
}

func TestTrimTailSkipsLineLimitWhenBudgetRaised(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 4000; i++ {
		fmt.Fprintf(&sb, "line-%d\n", i)
	}
	// A caller-raised byte budget means the default line cap no longer applies.
	got := trimTail(sb.String(), 1<<20, DefaultOutputLines)
	if got.Spilled || strings.Count(got.Text, "\n") != 4000 {
		t.Fatalf("raised budget: spilled=%v lines=%d, want all 4000 lines", got.Spilled, strings.Count(got.Text, "\n"))
	}
}
