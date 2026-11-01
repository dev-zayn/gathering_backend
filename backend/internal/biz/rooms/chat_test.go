package rooms

import (
	"strings"
	"testing"
)

func TestChatText(t *testing.T) {
	for in, want := range map[string]string{
		"  هلا   والله\nشلونك  ": "هلا والله شلونك",
		"a\u202eb\u0000c":        "abc",
		"يا كس":                  "يا ***",
		"كـسـسـس!":               "***!",
		"گحبة":                   "***",
		"والعير":                 "***",
		"كسك":                    "***",
		"FUCK you":               "*** you",
		"fuuuck":                 "***",
		"بكس سيارة لكزس نيكل عيرنا": "بكس سيارة لكزس نيكل عيرنا",
		"مكسور وكسرني":              "مكسور وكسرني",
		"class shitake":             "class shitake",
		"\u200d\U0001F468\u200d":    "\u200d\U0001F468\u200d",
	} {
		got, ok := ChatText(in)
		if !ok || got != want {
			t.Errorf("ChatText(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "   \n ", "\u202e", strings.Repeat("ا", MaxChatText+1)} {
		if got, ok := ChatText(in); ok {
			t.Errorf("ChatText(%q) = %q, was let through", in, got)
		}
	}
	if _, ok := ChatText(strings.Repeat("ا", MaxChatText)); !ok {
		t.Error("the longest message was refused")
	}
}

func TestChatEmote(t *testing.T) {
	if !ChatEmote("laughing") || ChatEmote("") || ChatEmote("<img>") {
		t.Fatal("the emotes are the list")
	}
}
