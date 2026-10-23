package model

// The two column types the models are made of, here because the model is the
// row. Nothing imports anything of ours: Scan, Value and struct tags are enough.

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// Strings is a short list in one jsonb column rather than a Postgres array:
// a game's scopes, its redirect URIs.
type Strings []string

func (s *Strings) Scan(src any) error { return scanJSON(src, s) }

// Value writes an empty list as [] and never as null: a column that is
// sometimes null and sometimes a list is two shapes to read back.
func (s Strings) Value() (driver.Value, error) {
	if s == nil {
		s = Strings{}
	}
	return valueJSON(s)
}

// Raw is a document we keep whole and do not interpret: a partner's manifest,
// exactly as it arrived.
type Raw []byte

func (r *Raw) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*r = nil
	case []byte:
		*r = append((*r)[:0], v...)
	case string:
		*r = []byte(v)
	default:
		return fmt.Errorf("model: cannot read %T as json", src)
	}
	return nil
}

func (r Raw) Value() (driver.Value, error) {
	if len(r) == 0 {
		return "{}", nil
	}
	return string(r), nil
}

// NullRaw is a document kept whole that may be absent: SQL NULL when empty,
// where Raw writes {}. A game's shared state is one, and "never written" is
// not the same as an empty object.
type NullRaw []byte

func (r *NullRaw) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*r = nil
	case []byte:
		*r = append((*r)[:0], v...)
	case string:
		*r = []byte(v)
	default:
		return fmt.Errorf("model: cannot read %T as json", src)
	}
	return nil
}

func (r NullRaw) Value() (driver.Value, error) {
	if len(r) == 0 {
		return nil, nil
	}
	return string(r), nil
}

func scanJSON(src, dst any) error {
	switch v := src.(type) {
	case nil:
		return nil
	case []byte:
		return json.Unmarshal(v, dst)
	case string:
		return json.Unmarshal([]byte(v), dst)
	default:
		return fmt.Errorf("model: cannot read %T as json", src)
	}
}

func valueJSON(v any) (driver.Value, error) {
	b, err := json.Marshal(v)
	return string(b), err
}
