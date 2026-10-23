// Command roomplayer is a headless member of a room, for a laptop: it signs
// a new player up on an api running with OTP_FIXED_CODE, joins a room by
// its code, and stays in it over the relay as a phone would — without a
// game page. In the tap race it sends its taps the way the page does
// (`msg {type: "tap"}`) inside the race window each round's start
// announces; in any other game it only sits at the table. It prints its
// place after every round, sits through the lobby between rounds, and after
// -rounds finished rounds it leaves the room and exits; a room that ends
// first ends it too. Its last line is its place in the last round it
// played. It is the third or the tenth player of a device test on a machine
// that cannot run another emulator.
//
//	go run ./cmd/roomplayer -code K7M2QX -taps 10 -rounds 2
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// The tap race's clock, as the game page keeps it: three seconds of count
// down from the start, then ten of racing.
const (
	countdown = 3 * time.Second
	race      = 10 * time.Second
)

func main() {
	api := flag.String("api", "http://localhost:8080", "the api's address")
	code := flag.String("code", "", "the room's six-symbol code")
	otp := flag.String("otp", "123456", "the api's OTP_FIXED_CODE")
	nickname := flag.String("nickname", "روبوت", "the player's nickname")
	taps := flag.Int("taps", 10, "taps to send in each round of the tap race")
	rounds := flag.Int("rounds", 1, "leave the room and exit after this many finished rounds")
	timeout := flag.Duration("timeout", 10*time.Minute, "give up after this long")
	flag.Parse()
	if *code == "" {
		log.Fatal("roomplayer: -code is required")
	}
	if *rounds < 1 {
		log.Fatal("roomplayer: -rounds is at least 1")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	p := &player{api: strings.TrimRight(*api, "/"), http: &http.Client{Timeout: 15 * time.Second}}
	if err := p.run(ctx, *code, *otp, *nickname, *taps, *rounds); err != nil {
		log.Fatalf("roomplayer: %v", err)
	}
}

type player struct {
	api     string
	http    *http.Client
	session string
	id      string
}

// place is where the player came in one round: found false when the round's
// results do not name them.
type place struct {
	round   int
	found   bool
	rank    int
	players int
	score   float64
}

func (p *player) run(ctx context.Context, code, otp, nickname string, taps, rounds int) error {
	if err := p.signUp(ctx, otp, nickname); err != nil {
		return fmt.Errorf("signing up: %w", err)
	}
	var room roomJSON
	if err := p.call(ctx, http.MethodPost, "/v1/rooms/join", map[string]any{"code": code}, &room); err != nil {
		return fmt.Errorf("joining %s: %w", code, err)
	}
	fmt.Printf("roomplayer: %s is in %s (%s, %s)\n", p.id, room.Code, room.Game.ID, room.Status)

	conn, err := p.connect(ctx, room.ID)
	if err != nil {
		return err
	}
	defer func() { _ = conn.CloseNow() }()

	go keepAlive(ctx, conn)

	// playing is the round the player is in, or was last in; tapped the last
	// round the taps went out for, so a frame heard twice never taps twice.
	playing, tapped := 0, 0
	startRound := func(round int, startedAt time.Time) {
		playing = round
		if round <= tapped || taps <= 0 || !strings.Contains(room.Game.ID, "tap") {
			return
		}
		tapped = round
		go tapRace(ctx, conn, startedAt, taps)
	}
	if room.Status == "playing" && room.StartedAt != nil {
		startRound(room.Round, *room.StartedAt)
	}

	var last *place
	finished := 0
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			var closed websocket.CloseError
			if errors.As(err, &closed) && closed.Code == 4004 {
				break // the room ended and the relay said goodbye
			}
			return fmt.Errorf("the relay: %w", err)
		}
		var f frame
		if err := json.Unmarshal(data, &f); err != nil {
			continue
		}
		switch f.T {
		case "started":
			fmt.Printf("roomplayer: round %d started\n", f.Round)
			if f.At != nil {
				startRound(f.Round, *f.At)
			}
		case "members":
			fmt.Printf("roomplayer: %d at the table\n", len(f.List))
		case "finished":
			if last, err = p.roundPlace(ctx, room.ID, f.Round); err != nil {
				return err
			}
			finished++
			if finished < rounds {
				continue
			}
			p.printFinal(last)
			if err := p.call(ctx, http.MethodPost, "/v1/rooms/"+room.ID+"/leave", nil, nil); err != nil {
				return fmt.Errorf("leaving: %w", err)
			}
			fmt.Printf("roomplayer: left %s after %d rounds\n", room.Code, finished)
			return nil
		case "ended":
			fmt.Printf("roomplayer: the room ended (%s)\n", f.Reason)
			// Closed during a round the player was in: that round is over too.
			if f.Round > 0 && f.Round == playing && (last == nil || last.round != f.Round) {
				if last, err = p.roundPlace(ctx, room.ID, f.Round); err != nil {
					return err
				}
			}
			p.printFinal(last)
			return nil
		}
	}
	p.printFinal(last)
	return nil
}

// roundPlace reads one round's results and prints the player's place in it.
func (p *player) roundPlace(ctx context.Context, roomID string, round int) (*place, error) {
	var sheet struct {
		Results []struct {
			Player string  `json:"player"`
			Score  float64 `json:"score"`
			Rank   int     `json:"rank"`
		} `json:"results"`
	}
	path := fmt.Sprintf("/v1/rooms/%s/results?round=%d", roomID, round)
	if err := p.call(ctx, http.MethodGet, path, nil, &sheet); err != nil {
		return nil, fmt.Errorf("reading round %d's results: %w", round, err)
	}
	out := &place{round: round, players: len(sheet.Results)}
	for _, r := range sheet.Results {
		if r.Player == p.id {
			out.found, out.rank, out.score = true, r.Rank, r.Score
			break
		}
	}
	if out.found {
		fmt.Printf("roomplayer: round %d: rank %d of %d\n", round, out.rank, out.players)
	} else {
		fmt.Printf("roomplayer: round %d: not in the %d results\n", round, out.players)
	}
	return out, nil
}

// printFinal is the last line: the player's place in the last round they
// played, which the device scripts read.
func (p *player) printFinal(last *place) {
	switch {
	case last == nil:
		fmt.Println("roomplayer: played no round")
	case last.found:
		fmt.Printf("roomplayer: rank %d of %d, score %g\n", last.rank, last.players, last.score)
	default:
		fmt.Printf("roomplayer: not in the %d results\n", last.players)
	}
}

// tapRace sends the taps spread through the race, a second clear of either
// end, so every phone's page is counting when they arrive.
func tapRace(ctx context.Context, conn *websocket.Conn, startedAt time.Time, taps int) {
	from := startedAt.Add(countdown + time.Second)
	until := startedAt.Add(countdown + race - 2*time.Second)
	step := until.Sub(from) / time.Duration(taps)
	msg := []byte(`{"t":"msg","data":{"type":"tap"}}`)
	for i := range taps {
		at := from.Add(time.Duration(i) * step)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(at)):
		}
		if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
			fmt.Printf("roomplayer: tap %d not sent: %v\n", i+1, err)
			return
		}
	}
	fmt.Printf("roomplayer: sent %d taps\n", taps)
}

// keepAlive pings every twenty seconds, as the app does, so the relay keeps
// the socket and the presence.
func keepAlive(ctx context.Context, conn *websocket.Conn) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := conn.Write(ctx, websocket.MessageText, []byte(`{"t":"ping"}`)); err != nil {
				return
			}
		}
	}
}

func (p *player) connect(ctx context.Context, roomID string) (*websocket.Conn, error) {
	url := "ws" + strings.TrimPrefix(p.api, "http") + "/v1/rooms/" + roomID + "/socket"
	conn, res, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		Subprotocols: []string{"gathering.v1"},
		HTTPHeader:   http.Header{"Authorization": {"Bearer " + p.session}},
	})
	if err != nil {
		status := 0
		if res != nil {
			status = res.StatusCode
		}
		return nil, fmt.Errorf("connecting to the relay (%d): %w", status, err)
	}
	conn.SetReadLimit(1 << 20)
	return conn, nil
}

// signUp is the app's sign-in for a number nobody has: the code, then a
// nickname, and a session.
func (p *player) signUp(ctx context.Context, otp, nickname string) error {
	var ticket struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
		Player      *struct {
			ID string `json:"id"`
		} `json:"player"`
	}
	if err := p.call(ctx, http.MethodPost, "/v1/auth/signin", map[string]any{"phone": randomPhone()}, &ticket); err != nil {
		return err
	}
	p.session = ticket.AccessToken
	if err := p.call(ctx, http.MethodPost, "/v1/auth/otp/verify", map[string]any{"code": otp}, &ticket); err != nil {
		return err
	}
	if ticket.Scope != "signup" {
		return fmt.Errorf("the number was already a player's (%s)", ticket.Scope)
	}
	p.session = ticket.AccessToken
	if err := p.call(ctx, http.MethodPost, "/v1/auth/signup", map[string]any{"nickname": nickname, "language": "ar"}, &ticket); err != nil {
		return err
	}
	p.session = ticket.AccessToken
	if ticket.Player == nil {
		return errors.New("signed up without a player")
	}
	p.id = ticket.Player.ID
	return nil
}

func (p *player) call(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.api+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.session != "" {
		req.Header.Set("Authorization", "Bearer "+p.session)
	}
	res, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d %s", method, path, res.StatusCode, bytes.TrimSpace(raw))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// randomPhone is a fresh Iraqi mobile number, so every run is a new player.
func randomPhone() string {
	n, err := rand.Int(rand.Reader, big.NewInt(10_000_000))
	if err != nil {
		fmt.Fprintln(os.Stderr, "roomplayer: no randomness:", err)
		os.Exit(1)
	}
	return fmt.Sprintf("0771%07d", n.Int64())
}

type roomJSON struct {
	ID        string     `json:"id"`
	Code      string     `json:"code"`
	Status    string     `json:"status"`
	Round     int        `json:"round"`
	StartedAt *time.Time `json:"started_at"`
	Game      struct {
		ID string `json:"id"`
	} `json:"game"`
}

type frame struct {
	T      string            `json:"t"`
	At     *time.Time        `json:"at"`
	Round  int               `json:"round"`
	Reason string            `json:"reason"`
	List   []json.RawMessage `json:"list"`
}
