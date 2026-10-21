package player

import (
	"time"

	"github.com/jamaa/gathering/backend/internal/data/model"
)

type noContent struct{}

// playerJSON is a player as others see them: the public card.
type playerJSON struct {
	ID       string `json:"id" example:"ply_7f3k9d2m8q1x4b6n"`
	Nickname string `json:"nickname"`
	Handle   string `json:"handle" example:"zayn_4821" doc:"The @name friends search by."`
	Avatar   string `json:"avatar" doc:"A bundled avatar id, or empty."`
}

// meJSON is the player's own record: the card plus what only they see.
type meJSON struct {
	playerJSON
	Phone     string `json:"phone,omitempty" doc:"E.164."`
	Bio       string `json:"bio"`
	Language  string `json:"language" enum:"ar,en"`
	CreatedAt string `json:"created_at"`
}

func card(p *model.Player) playerJSON {
	return playerJSON{ID: p.ID, Nickname: p.Nickname, Handle: p.Handle, Avatar: p.AvatarKey}
}

func me(p *model.Player) meJSON {
	out := meJSON{
		playerJSON: card(p),
		Bio:        p.Bio,
		Language:   string(p.Language),
		CreatedAt:  stamp(p.CreatedAt),
	}
	if p.Phone != nil {
		out.Phone = *p.Phone
	}
	return out
}

// stamp is the one timestamp format this API answers in: UTC, to the second.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }
