package admin

import "time"

// noContent is a 204. Each audience declares its own rather than sharing one,
// which is three lines against an import between two packages.
type noContent struct{}

// playerPath is the player id in the URL.
type playerPath struct {
	Player string `path:"player" example:"ply_5pt1kx8vqzr3nayd"`
}

type adminJSON struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Email string   `json:"email"`
	Roles []string `json:"roles"`
}

// stamp is the one timestamp format this API answers in: UTC, to the second.
func stamp(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format("2006-01-02T15:04:05Z")
	return &s
}

// nonNil keeps a JSON array an array. A client should not have to tell null
// from empty.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
