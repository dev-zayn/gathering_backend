package fcm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIsFIDReadsAnInstallationID(t *testing.T) {
	for _, s := range []string{
		"cXyZ0123456789abcdEF_-", // 22, starts c
		"dAAAAAAAAAAAAAAAAAAAAA",
		"f_-0123456789abcdefghi",
	} {
		if !IsFID(s) {
			t.Errorf("IsFID(%q) = false, want true", s)
		}
	}
	for _, s := range []string{
		"",
		"cXyZ0123456789abcdEF_",   // 21
		"cXyZ0123456789abcdEF_-a", // 23
		"aXyZ0123456789abcdEF_-",  // first symbol outside c..f
		"gXyZ0123456789abcdEF_-",
		"cXyZ0123456789abcdEF:-", // a token's colon
		"cXyZ0123456789abcdEF/+", // standard base64, not URL-safe
		"fYbbaAAAAAA:APA91bH" + strings.Repeat("x", 140),
	} {
		if IsFID(s) {
			t.Errorf("IsFID(%q) = true, want false", s)
		}
	}
}

func TestMessageGoesToTheFIDOrTheToken(t *testing.T) {
	fid := "cXyZ0123456789abcdEF_-"
	var m Message
	m.To(fid)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); !strings.Contains(got, `"fid":"`+fid+`"`) || strings.Contains(got, `"token"`) {
		t.Errorf("an FID went as %s", got)
	}
	if m.Target() != fid {
		t.Errorf("Target() = %q, want the FID", m.Target())
	}

	token := "fYbbaAAAAAA:APA91bH" + strings.Repeat("x", 140)
	m.To(token)
	b, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); !strings.Contains(got, `"token":"`+token+`"`) || strings.Contains(got, `"fid"`) {
		t.Errorf("a registration token went as %s", got)
	}
	if m.Target() != token {
		t.Errorf("Target() = %q, want the token", m.Target())
	}
}
