package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

// A room's keys carry its id in braces: a Redis Cluster hash tag, so every
// key of one room lands in one slot and one script may touch them all.
//
//	room:{room_…}:seq    the frame counter
//	room:{room_…}:log    the last History frames
//	room:{room_…}:ver    the shared state's version
//	room:{room_…}:data   the shared state's data
//	room:{room_…}:touch  the active_at throttle
//	room:{room_…}        the channel the live frames go out on
func seqKey(room string) string   { return "room:{" + room + "}:seq" }
func logKey(room string) string   { return "room:{" + room + "}:log" }
func verKey(room string) string   { return "room:{" + room + "}:ver" }
func dataKey(room string) string  { return "room:{" + room + "}:data" }
func touchKey(room string) string { return "room:{" + room + "}:touch" }
func channel(room string) string  { return "room:{" + room + "}" }

// roomOf reads a channel's name back into the room's id.
func roomOf(ch string) string {
	return strings.TrimSuffix(strings.TrimPrefix(ch, "room:{"), "}")
}

// keys is the four a script touches, in the order the scripts name them.
func keys(room string) []string {
	return []string{seqKey(room), logKey(room), verKey(room), dataKey(room)}
}

// publishLua appends one frame to the room's history. KEYS: seq, log, ver,
// data. ARGV: the channel, the frame up to and including its type, the rest
// of the frame, the keys' TTL in seconds, the history's length. The seq is
// spliced in after the type, so the frame reads {"t":…,"seq":N,…}; every key
// of the room is kept alive while anything is said in it.
const publishLua = `
local seq = redis.call('INCR', KEYS[1])
local frame = ARGV[2] .. ',"seq":' .. seq .. ARGV[3]
redis.call('RPUSH', KEYS[2], frame)
redis.call('LTRIM', KEYS[2], -tonumber(ARGV[5]), -1)
for i = 1, #KEYS do redis.call('EXPIRE', KEYS[i], ARGV[4]) end
redis.call('PUBLISH', ARGV[1], frame)
return seq
`

// publishScript is publishLua: every history frame but a round's start.
var publishScript = redis.NewScript(publishLua)

// startScript is a round's start: the shared state back to version 0 with
// no data, then the started frame published as publishLua publishes every
// frame, in one step. A state write either landed before the reset, and its
// frame comes before started, or it is judged against version 0. KEYS and
// ARGV are publishLua's.
var startScript = redis.NewScript(`
redis.call('SET', KEYS[3], '0')
redis.call('DEL', KEYS[4])
` + publishLua)

// stateScript is a compare-and-set on the shared state, and the publish of
// the result, in one step. KEYS: seq, log, ver, data. ARGV: the channel, the
// version the write was based on, the data, the frame up to its type, the
// rest of the frame after the version, the TTL, the history's length.
// Answers {1, version, seq} when the write landed, {0, current} when it was
// based on an old version.
var stateScript = redis.NewScript(`
local current = tonumber(redis.call('GET', KEYS[3]) or '0')
if current ~= tonumber(ARGV[2]) then return {0, current} end
local version = current + 1
redis.call('SET', KEYS[3], version)
redis.call('SET', KEYS[4], ARGV[3])
local seq = redis.call('INCR', KEYS[1])
local frame = ARGV[4] .. ',"seq":' .. seq .. ',"version":' .. version .. ARGV[5]
redis.call('RPUSH', KEYS[2], frame)
redis.call('LTRIM', KEYS[2], -tonumber(ARGV[7]), -1)
for i = 1, #KEYS do redis.call('EXPIRE', KEYS[i], ARGV[6]) end
redis.call('PUBLISH', ARGV[1], frame)
return {1, version, seq}
`)

// typePrefix is how every frame begins: the type is the first field.
const typePrefix = `{"t":"`

// split marshals a frame and cuts it after its type, where the script
// splices the seq in. A frame that does not begin with its type is a
// programming error, refused rather than published wrong.
func split(frame any) (head, tail string, err error) {
	b, err := json.Marshal(frame)
	if err != nil {
		return "", "", err
	}
	head, tail, ok := cut(b)
	if !ok {
		return "", "", fmt.Errorf("relay: a frame must begin with its type: %.40s", b)
	}
	return head, tail, nil
}

// cut splits marshalled JSON after the value of its leading "t". Types are
// plain words, so the value ends at the next quote.
func cut(b []byte) (head, tail string, ok bool) {
	if !bytes.HasPrefix(b, []byte(typePrefix)) {
		return "", "", false
	}
	end := bytes.IndexByte(b[len(typePrefix):], '"')
	if end < 0 {
		return "", "", false
	}
	at := len(typePrefix) + end + 1
	return string(b[:at]), string(b[at:]), true
}
