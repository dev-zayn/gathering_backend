package caller

import (
	"encoding/json"
	"fmt"
)

// Audience is the aud claim, which RFC 7519 allows as a string or an array. This
// reads either and writes a bare string when there is one, as libraries expect.
type Audience []string

func (a *Audience) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*a = Audience{one}
		return nil
	}

	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("identity: aud is neither a string nor a list of them: %w", err)
	}
	*a = many
	return nil
}

func (a Audience) MarshalJSON() ([]byte, error) {
	if len(a) == 1 {
		return json.Marshal(a[0])
	}
	return json.Marshal([]string(a))
}
