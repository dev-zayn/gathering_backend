package guest

import "testing"

// docs/JOINING.md: English when Accept-Language prefers en over ar, by
// weight, then by order; only en and ar count, q=0 refuses; Arabic
// otherwise.
func TestPrefersEnglish(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   bool
	}{
		{"", false},
		{"ar", false},
		{"en", true},
		{"EN-gb", true},
		{"en_US", true},
		{"en-US,en;q=0.9,ar;q=0.8", true},
		{"ar-IQ,ar;q=0.9,en-US;q=0.8,en;q=0.7", false},
		{"en;q=0.8,ar;q=0.8", true},         // the same weight: the first named
		{"ar;q=0.8,en;q=0.8", false},        // and the other way round
		{"ar;q=0.5,en-GB;q=0.6", true},      // the weight, not the order
		{" en ; q=0.9 , ar ; q=0.5 ", true}, // spaces anywhere
		{"fr-FR,fr;q=0.9", false},           // neither named
		{"fr,en;q=0.5", true},               // English named, Arabic not
		{"en;q=0", false},                   // refused
		{"en;q=0,ar;q=0", false},
		{"*", false}, // counts for neither
		{"*,en;q=0.1", true},
		{"en;q=abc,ar;q=0.1", false}, // an unreadable weight is none
		{"en;q=2", false},            // and so is one out of range
		{"english", false},           // not a tag of en
	} {
		if got := prefersEnglish(tc.header); got != tc.want {
			t.Errorf("%q: English %v, want %v", tc.header, got, tc.want)
		}
	}
}

// The policy names the icon's host as the one place a picture may come
// from, and only a host it can name as it is.
func TestOrigin(t *testing.T) {
	for _, tc := range []struct{ link, want string }{
		{"https://signed.test/games/fixture.xo/icon.png", "https://signed.test"},
		{"http://localhost:8080/files/games/fixture.xo/icon.png", "http://localhost:8080"},
		{"https://bucket.s3.me-south-1.amazonaws.com/games/x/icon.png?X-Amz-Signature=ab%2Fcd&X-Amz-Expires=3600",
			"https://bucket.s3.me-south-1.amazonaws.com"},
		{"https://someone:secret@signed.test/x.png", "https://signed.test"},
		{"", ""},
		{"/files/x.png", ""},
		{"//signed.test/x.png", ""},
		{"data:image/png;base64,iVBORw0KGgo=", ""},
		{"javascript:alert(1)", ""},
		{"ftp://signed.test/x.png", ""},
		{"https://a;b.test/x.png", ""},
		{"https://a'b.test/x.png", ""},
	} {
		if got := origin(tc.link); got != tc.want {
			t.Errorf("%q: %q, want %q", tc.link, got, tc.want)
		}
	}
}
