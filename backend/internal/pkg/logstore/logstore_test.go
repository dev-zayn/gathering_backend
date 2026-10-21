package logstore

import "testing"

// The queries run against a real database in biz/audit, which may open one.

func TestClipKeepsWholeCharacters(t *testing.T) {
	if got := clip("aéb", 2); got != "aé" {
		t.Errorf("clip counted bytes, not characters: %q", got)
	}
	if got := clip("short", 64); got != "short" {
		t.Errorf("clip changed %q", got)
	}
}

// A % in a search is the character, not "anything".
func TestContainsEscapesWildcards(t *testing.T) {
	if got := contains(`50%_\`); got != `%50\%\_\\%` {
		t.Errorf("pattern is %q", got)
	}
}

// A page is what was asked for within reason, and the default otherwise.
func TestLimitOf(t *testing.T) {
	for requested, want := range map[int]int{0: 50, -1: 50, 20: 20, 200: 200, 201: 50} {
		if got := LimitOf(requested); got != want {
			t.Errorf("LimitOf(%d) = %d, want %d", requested, got, want)
		}
	}
}
