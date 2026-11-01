package rooms

import (
	_ "embed"
	"strings"
	"unicode"
)

// MaxChatText is the most runes one chat message may hold, once cleaned.
const MaxChatText = 200

// chatEmotes are the die's poses a room may send, docs/ROOMS.md's list. An
// emote is a name and never a picture, so nothing goes through the relay that
// the app did not ship.
var chatEmotes = map[string]bool{
	"wave": true, "laughing": true, "cheer": true, "crown": true, "thinking": true,
	"surprised": true, "angry": true, "shades": true, "wink": true, "running": true,
}

// ChatEmote reports whether a name is one of the emotes.
func ChatEmote(name string) bool { return chatEmotes[name] }

// ChatText is what a player typed as the room reads it: on one line, without
// the characters that can hide or reorder text, its bad words masked. False
// when nothing is left, or too much.
func ChatText(raw string) (string, bool) {
	text := maskBadWords(strings.Join(strings.FieldsFunc(clean(raw), unicode.IsSpace), " "))
	n := len([]rune(text))
	return text, strings.Trim(text, "\u200c\u200d") != "" && n <= MaxChatText
}

// clean drops control characters and the format characters, all but the two
// joiners that Arabic and emoji need.
func clean(raw string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case r == '\u200c' || r == '\u200d':
			return r
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, raw)
}

//go:embed chat_words.txt
var chatWordList string

// chatWords is chat_words.txt, as normal reads them.
var chatWords = func() map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(chatWordList, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[normal(line)] = true
	}
	return out
}()

// maskBadWords puts stars over every word the list holds. It compares whole
// words, because a part of an innocent Arabic word is often a bad one.
func maskBadWords(text string) string {
	var out, word strings.Builder
	flush := func() {
		if word.Len() == 0 {
			return
		}
		if isBad(normal(word.String())) {
			out.WriteString("***")
		} else {
			out.WriteString(word.String())
		}
		word.Reset()
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) {
			word.WriteRune(r)
			continue
		}
		flush()
		out.WriteRune(r)
	}
	flush()
	return out.String()
}

// isBad is a word on the list, as it stands or with the commonest Arabic ends
// taken off: "and", "the", and "your", "his", "my".
func isBad(word string) bool {
	for _, w := range []string{word, cutAny(word, "وال", "ال", "و")} {
		if chatWords[w] {
			return true
		}
		for _, end := range []string{"كم", "ك", "ه", "ي"} {
			if s, ok := strings.CutSuffix(w, end); ok && chatWords[s] {
				return true
			}
		}
	}
	return false
}

func cutAny(word string, prefixes ...string) string {
	for _, p := range prefixes {
		if s, ok := strings.CutPrefix(word, p); ok {
			return s
		}
	}
	return word
}

// normal is a word without what is written to dodge a list: lower case, no
// marks or stretching, one spelling of each letter family, and no letter
// twice in a row.
func normal(word string) string {
	var out strings.Builder
	var last rune
	for _, r := range strings.ToLower(word) {
		switch r {
		case 'أ', 'إ', 'آ':
			r = 'ا'
		case 'ى':
			r = 'ي'
		case 'ة':
			r = 'ه'
		case 'ؤ':
			r = 'و'
		case 'ئ':
			r = 'ي'
		case 'گ', 'ق':
			r = 'ك'
		case 'چ':
			r = 'ج'
		case 'ـ':
			continue
		}
		if unicode.IsMark(r) || r == last {
			continue
		}
		last = r
		out.WriteRune(r)
	}
	return out.String()
}
