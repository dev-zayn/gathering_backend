package tournaments

// What a tournament tells its players (docs/TOURNAMENTS.md,
// "Notifications"): one kind, tournament, in the platform's own words, each
// keyed so a repeat updates its row instead of piling up.

import (
	"context"
	"fmt"

	"github.com/jamaa/gathering/backend/internal/biz/inbox"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

// notice is one notification's words, its key, and the match it is about,
// if one.
type notice struct {
	title, body, key, match string
}

func inviteNotice(inviter, name, game string) notice {
	return notice{title: "دعوة لبطولة", body: fmt.Sprintf("%s يدعوك لبطولة %s (%s)", inviter, name, game), key: "tournament_invite:"}
}

func startedNotice(t *model.Tournament) notice {
	body := fmt.Sprintf("%s: القرعة صارت، شوف مباراتك", t.Name)
	if t.Format == enum.FormatLeague {
		body = fmt.Sprintf("%s: الدوري بدأ", t.Name)
	}
	return notice{title: "بدأت البطولة", body: body, key: "tournament_started:"}
}

func matchNotice(t *model.Tournament, m *model.TournamentMatch, opponent string) notice {
	return notice{
		title: "مباراتك جاهزة",
		body:  fmt.Sprintf("ضد %s · %s · %s", opponent, stageWords(m.Stage, t.Stages), t.Name),
		key:   "tournament_match:" + m.ID, match: m.ID,
	}
}

func doneNotice(t *model.Tournament, champion string, mine bool) notice {
	n := notice{title: "انتهت البطولة", key: "tournament_done:"}
	switch {
	case mine:
		n.body = fmt.Sprintf("مبروك! صرت بطل %s", t.Name)
	case champion != "":
		n.body = fmt.Sprintf("%s بطل %s", champion, t.Name)
	default:
		n.body = fmt.Sprintf("%s خلصت", t.Name)
	}
	return n
}

func cancelledNotice(t *model.Tournament) notice {
	return notice{title: "ألغيت البطولة", body: fmt.Sprintf("%s ما راح تكمل", t.Name), key: "tournament_cancelled:"}
}

// stageWords is a knockout stage as a player says it: the final, the
// semi-finals, the quarter-finals, else the stage's number.
func stageWords(stage, stages int) string {
	switch stages - stage {
	case 0:
		return "النهائي"
	case 1:
		return "نصف النهائي"
	case 2:
		return "ربع النهائي"
	}
	return fmt.Sprintf("الدور %d", stage)
}

// gameName is the game's name, or its id when its row is gone.
func gameName(g *model.Game, id string) string {
	if g == nil || g.Name == "" {
		return id
	}
	return g.Name
}

// raise puts one notification in a player's inbox, in the caller's
// transaction. A key ending in ":" is the tournament's own (its id follows);
// a match's key carries the match.
func (s *Service) raise(ctx context.Context, t *model.Tournament, to, actor string, n notice) error {
	if s.Inbox == nil {
		return nil
	}
	key := n.key
	if key != "" && key[len(key)-1] == ':' {
		key += t.ID
	}
	fields := map[string]any{"tournament": t.ID, "code": t.Code, "game": t.GameID}
	if n.match != "" {
		fields["match"] = n.match
	}
	_, err := s.Inbox.Raise(ctx, inbox.Notice{
		PlayerID: to, Kind: enum.NoticeTournament, ActorID: actor, GameID: t.GameID,
		Title: n.title, Body: n.body, DeepLink: "/t/" + t.Code, Data: fields, Key: key,
	})
	return err
}

// commit writes the state back, then tells the players what it did — the
// matches that turned ready, the start, the end — and has the rooms of the
// matches it decided closed once it has committed. In the caller's
// transaction.
func (s *Service) commit(ctx context.Context, st *state) error {
	if err := st.save(ctx, s.db); err != nil {
		return err
	}
	if err := s.announce(ctx, st); err != nil {
		return err
	}
	s.closeRooms(ctx, st.closing)
	return nil
}

// announce raises what the change calls for. A knockout match that turned
// ready tells both its players; the start tells every entrant who was not
// just told of a match; the end tells everybody who played.
func (s *Service) announce(ctx context.Context, st *state) error {
	if s.Inbox == nil || (len(st.ready) == 0 && !st.began && !st.ended) {
		return nil
	}
	t := st.t
	names, err := s.nicknames(ctx, st)
	if err != nil {
		return err
	}
	told := map[string]bool{}
	if t.Format == enum.FormatKnockout {
		for _, m := range st.ready {
			if m.Status != enum.MatchReady || m.PlayerA == nil || m.PlayerB == nil {
				continue
			}
			for _, pair := range [][2]string{{*m.PlayerA, *m.PlayerB}, {*m.PlayerB, *m.PlayerA}} {
				if err := s.raise(ctx, t, pair[0], "", matchNotice(t, m, names[pair[1]])); err != nil {
					return err
				}
				told[pair[0]] = true
			}
		}
	}
	if st.began {
		for _, e := range st.current() {
			if told[e.PlayerID] {
				continue
			}
			if err := s.raise(ctx, t, e.PlayerID, "", startedNotice(t)); err != nil {
				return err
			}
		}
	}
	if !st.ended {
		return nil
	}
	champion := ""
	if t.ChampionID != nil {
		champion = names[*t.ChampionID]
	}
	for _, e := range st.entrants {
		if e.Removed {
			continue
		}
		n := cancelledNotice(t)
		if t.Status == enum.TournamentDone {
			n = doneNotice(t, champion, t.ChampionID != nil && *t.ChampionID == e.PlayerID)
		}
		if err := s.raise(ctx, t, e.PlayerID, "", n); err != nil {
			return err
		}
	}
	return nil
}

// nicknames is every entrant's name, by id, read once.
func (s *Service) nicknames(ctx context.Context, st *state) (map[string]string, error) {
	ids := make([]string, 0, len(st.entrants))
	for _, e := range st.entrants {
		ids = append(ids, e.PlayerID)
	}
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID       string
		Nickname string
	}
	if err := data.Conn(ctx, s.db).Raw(`SELECT id, nickname FROM players WHERE id IN ?`, ids).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = r.Nickname
	}
	return out, nil
}
