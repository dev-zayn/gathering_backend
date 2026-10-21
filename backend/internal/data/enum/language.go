package enum

// Language is what the app renders in. A player sets it about themselves and
// nothing else reads it but the copy.
type Language string

const (
	Arabic  Language = "ar"
	English Language = "en"
)

var Languages = []Language{Arabic, English}

func (l Language) Valid() bool { return in(Languages, l) }
