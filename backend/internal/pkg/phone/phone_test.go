package phone

import "testing"

func TestNormalize(t *testing.T) {
	ok := map[string]string{
		"07701234567":       "+9647701234567",
		"7701234567":        "+9647701234567",
		"+964 770 123 4567": "+9647701234567",
		"00964-7701234567":  "+9647701234567",
		"9647701234567":     "+9647701234567",
		"0750 123 4567":     "+9647501234567",
	}
	for in, want := range ok {
		got, err := Normalize(in)
		if err != nil {
			t.Errorf("Normalize(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}

	bad := []string{"", "0770123456", "077012345678", "06701234567", "07a01234567", "+1 555 0100"}
	for _, in := range bad {
		if got, err := Normalize(in); err == nil {
			t.Errorf("Normalize(%q) = %q, want an error", in, got)
		}
	}
}

func TestMask(t *testing.T) {
	if got, want := Mask("+9647701234567"), "+964770*****67"; got != want {
		t.Errorf("Mask = %q, want %q", got, want)
	}
}
