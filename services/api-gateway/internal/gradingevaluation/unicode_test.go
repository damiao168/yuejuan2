package gradingevaluation

import (
	"strings"
	"testing"
)

func TestSafeTextCountsRunes(t *testing.T) {
	for _, char := range []string{"a", "你", "🙂"} {
		if !safeText("  "+strings.Repeat(char, 2000)+"  ", 2000) {
			t.Fatalf("valid text char=%s rejected", char)
		}
		if safeText(strings.Repeat(char, 2001), 2000) {
			t.Fatalf("long text char=%s accepted", char)
		}
	}
	if safeText(" \n\t", 2000) {
		t.Fatal("blank text accepted")
	}
}
