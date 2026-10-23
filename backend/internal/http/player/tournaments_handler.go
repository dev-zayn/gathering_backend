package player

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/tournaments"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

// Tournaments: making one, joining it by code, the organizer's start,
// finish, cancel, award and remove, and a player sitting down to their
// match, which answers the match's room. docs/TOURNAMENTS.md is the
// contract.

type tournamentResponse struct {
	Body *tournaments.Tournament
}

type tournamentPath struct {
	Tournament string `path:"tournament" maxLength:"24" example:"trn_7f3k9d2m8q1x4b6n"`
}

type tournamentMatchPath struct {
	Tournament string `path:"tournament" maxLength:"24" example:"trn_7f3k9d2m8q1x4b6n"`
	Match      string `path:"match" maxLength:"24" example:"trm_7f3k9d2m8q1x4b6n"`
}

type tournamentCreateRequest struct {
	Body struct {
		Game       string `json:"game" required:"true" maxLength:"64" example:"fixture.xo"`
		Name       string `json:"name,omitempty" maxLength:"200" example:"بطولة الجمعة" doc:"1 to 40 characters once trimmed (name: required, name: too_long)."`
		Format     string `json:"format,omitempty" example:"knockout" doc:"knockout or league (format: unknown); knockout needs a game that seats a duel (format: needs_duels)."`
		BestOf     int    `json:"best_of,omitempty" example:"3" doc:"Knockout: 1, 3 or 5, 1 by default (best_of: 1_3_or_5)."`
		Rounds     int    `json:"rounds,omitempty" example:"5" doc:"League: 1 to 20, 5 by default (rounds: 1_to_20)."`
		Size       int    `json:"size,omitempty" example:"8" doc:"The most entrants: knockout 2 to 64, 8 by default; league from two (or the game's minimum) to the game's table, the table by default (size: out_of_range)."`
		Visibility string `json:"visibility,omitempty" example:"private" doc:"private (the default: anybody with the code) or friends (the organizer's friends alone). public is the console's (visibility: not_allowed)."`
	}
}

type tournamentsRequest struct {
	View   string `query:"view" example:"mine" doc:"mine (the default: the caller is an entrant or the organizer) or public (the official ones) (view: unknown)."`
	State  string `query:"state" example:"active" doc:"active (the default: open and running) or past (done and cancelled) (state: unknown)."`
	Game   string `query:"game" example:"fixture.xo" doc:"One game's."`
	Cursor string `query:"cursor" doc:"next_cursor from the page before; leave it out for the first page (cursor: invalid)."`
	Limit  int    `query:"limit" default:"20" doc:"1 to 50 (limit: 1_to_50)."`
}

type tournamentsResponse struct {
	Body *tournaments.TournamentPage
}

type tournamentCodePath struct {
	Code string `path:"code" maxLength:"64" example:"T4K9QX" doc:"Six symbols. Case, spaces and dashes do not count."`
}

type tournamentJoinRequest struct {
	Body struct {
		Code string `json:"code" required:"true" maxLength:"64" example:"T4K-9QX" doc:"Six symbols. Case, spaces and dashes do not count."`
	}
}

type tournamentPlayerRequest struct {
	Tournament string `path:"tournament" maxLength:"24" example:"trn_7f3k9d2m8q1x4b6n"`
	Body       struct {
		Player string `json:"player" required:"true" maxLength:"24" example:"ply_7f3k9d2m8q1x4b6n"`
	}
}

type tournamentAwardRequest struct {
	Tournament string `path:"tournament" maxLength:"24" example:"trn_7f3k9d2m8q1x4b6n"`
	Match      string `path:"match" maxLength:"24" example:"trm_7f3k9d2m8q1x4b6n"`
	Body       struct {
		Player string `json:"player" required:"true" maxLength:"24" example:"ply_7f3k9d2m8q1x4b6n" doc:"One of the match's two players (player: not_in_match)."`
	}
}

type tournamentInviteRequest struct {
	Tournament string `path:"tournament" maxLength:"24" example:"trn_7f3k9d2m8q1x4b6n"`
	Body       struct {
		Players []string `json:"players" required:"true" doc:"1 to 20 player ids (players: 1_to_20), each the caller's friend and not an entrant yet."`
	}
}

type tournamentInviteResponse struct {
	Body struct {
		Invited []string `json:"invited" doc:"The players invited, each once."`
	}
}

func (h *Handler) registerTournaments(api huma.API) {
	session := map[string]any{authn.ScopeKey: caller.ScopeSession}
	op := func(id, method, path, summary, description string, status int) huma.Operation {
		return huma.Operation{
			OperationID: id, Method: method, Path: path, Summary: summary, Description: description,
			Tags: []string{"tournaments"}, Metadata: session, DefaultStatus: status,
		}
	}

	huma.Register(api, op("tournament-create", http.MethodPost, "/v1/tournaments", "Make a tournament",
		"A knockout of duels or a league at one table for a published game that seats two, with the caller "+
			"as its organizer and first entrant and a six-symbol code to share. Every problem with the body "+
			"at once in invalid_fields; not_found for a game that is not published.", http.StatusCreated), h.createTournament)

	huma.Register(api, op("tournaments", http.MethodGet, "/v1/tournaments", "Tournaments",
		"The caller's (view mine: an entrant or the organizer) or the official ones (view public), active "+
			"(open and running) or past (done and cancelled), newest first, a page at a time: hand next_cursor "+
			"back as cursor until it is null. your_match is the caller's match still to be played.", 0), h.tournaments)

	huma.Register(api, op("tournament-by-code", http.MethodGet, "/v1/tournaments/code/{code}", "A tournament by its code",
		"The open or running tournament holding the code, for anybody holding it. tournament_not_found, or "+
			"tournament_closed when a finished one held it.", 0), h.tournamentByCode)

	huma.Register(api, op("tournament-join", http.MethodPost, "/v1/tournaments/join", "Join a tournament by code",
		"An entrant gets it back. removed (the organizer removed the caller, for good), friends_only, "+
			"tournament_started, tournament_full; tournament_not_found and tournament_closed as by code.", 0), h.joinTournament)

	huma.Register(api, op("tournament", http.MethodGet, "/v1/tournaments/{tournament}", "One tournament",
		"For its organizer and its entrants, anybody for an official one, the organizer's friends for a "+
			"friends one; not_found for everybody else.", 0), h.tournament)

	huma.Register(api, op("tournament-leave", http.MethodPost, "/v1/tournaments/{tournament}/leave", "Leave a tournament",
		"While it is open the caller is out and may come back with the code; once it runs it is a "+
			"withdrawal and every match still to play is lost. not_entrant, tournament_closed.", 0), h.leaveTournament)

	huma.Register(api, op("tournament-remove", http.MethodPost, "/v1/tournaments/{tournament}/remove", "Remove an entrant",
		"The organizer's: as the entrant's own leave, and for good. organizer_only, tournament_closed, "+
			"player: not_an_entrant / is_you.", 0), h.removeEntrant)

	huma.Register(api, op("tournament-invite", http.MethodPost, "/v1/tournaments/{tournament}/invites", "Invite friends",
		"A current entrant or the organizer, while it is open: each friend gets a tournament notification whose "+
			"link, /t/{code}, joins by the code. players.N: already_entrant or not_friends, every problem at once "+
			"and nobody invited unless all pass. not_entrant, tournament_started, tournament_closed.", 0), h.inviteToTournament)

	huma.Register(api, op("tournament-start", http.MethodPost, "/v1/tournaments/{tournament}/start", "Start it: the draw",
		"The organizer's, from open, with at least two entrants (a league: the game's minimum if more): the "+
			"seeds drawn at random, the knockout's bracket with its byes through or the league's match, and "+
			"every entrant told. organizer_only, tournament_started, tournament_closed, too_few_entrants.", 0), h.startTournament)

	huma.Register(api, op("tournament-cancel", http.MethodPost, "/v1/tournaments/{tournament}/cancel", "Call it off",
		"The organizer's, open or running, for good: no champion, and every match room closes. "+
			"organizer_only, tournament_closed.", 0), h.cancelTournament)

	huma.Register(api, op("tournament-finish", http.MethodPost, "/v1/tournaments/{tournament}/finish", "Finish a league now",
		"The organizer's: the league's table as it stands decides, with at least one round counted. "+
			"organizer_only, tournament_not_running, tournament_closed, not_league, nothing_played.", 0), h.finishTournament)

	huma.Register(api, op("tournament-match-play", http.MethodPost, "/v1/tournaments/{tournament}/matches/{match}/play",
		"Play my match",
		"The match's open room, joined, or a new one with the caller as its host: answers the Room, its "+
			"tournament filled. tournament_not_running, tournament_closed, not_in_match, match_waiting, "+
			"match_decided, then the rooms' refusals (in_another_room names the room the caller is still in).", 0), h.playMatch)

	huma.Register(api, op("tournament-match-award", http.MethodPost, "/v1/tournaments/{tournament}/matches/{match}/award",
		"Decide a match",
		"The organizer's: a ready knockout match decided for one of its two players, its room closed. "+
			"organizer_only, tournament_not_running, tournament_closed, not_knockout, match_waiting, "+
			"match_decided, player: not_in_match.", 0), h.awardMatch)
}

func (h *Handler) createTournament(ctx context.Context, in *tournamentCreateRequest) (*tournamentResponse, error) {
	t, err := h.Tournaments.Create(ctx, caller.FromContext(ctx).Subject, tournaments.CreateRequest{
		Game: in.Body.Game, Name: in.Body.Name, Format: in.Body.Format, BestOf: in.Body.BestOf,
		Rounds: in.Body.Rounds, Size: in.Body.Size, Visibility: in.Body.Visibility,
	})
	if err != nil {
		return nil, err
	}
	return &tournamentResponse{Body: t}, nil
}

func (h *Handler) tournaments(ctx context.Context, in *tournamentsRequest) (*tournamentsResponse, error) {
	page, err := h.Tournaments.List(ctx, caller.FromContext(ctx).Subject, tournaments.ListQuery{
		View: in.View, State: in.State, Game: in.Game, Cursor: in.Cursor, Limit: in.Limit,
	})
	if err != nil {
		return nil, err
	}
	return &tournamentsResponse{Body: page}, nil
}

func (h *Handler) tournamentByCode(ctx context.Context, in *tournamentCodePath) (*tournamentResponse, error) {
	t, err := h.Tournaments.ByCode(ctx, caller.FromContext(ctx).Subject, in.Code)
	if err != nil {
		return nil, err
	}
	return &tournamentResponse{Body: t}, nil
}

func (h *Handler) joinTournament(ctx context.Context, in *tournamentJoinRequest) (*tournamentResponse, error) {
	t, err := h.Tournaments.Join(ctx, caller.FromContext(ctx).Subject, in.Body.Code)
	if err != nil {
		return nil, err
	}
	return &tournamentResponse{Body: t}, nil
}

func (h *Handler) tournament(ctx context.Context, in *tournamentPath) (*tournamentResponse, error) {
	t, err := h.Tournaments.Get(ctx, tournaments.AsPlayer(caller.FromContext(ctx).Subject), in.Tournament)
	if err != nil {
		return nil, err
	}
	return &tournamentResponse{Body: t}, nil
}

func (h *Handler) leaveTournament(ctx context.Context, in *tournamentPath) (*tournamentResponse, error) {
	t, err := h.Tournaments.Leave(ctx, caller.FromContext(ctx).Subject, in.Tournament)
	if err != nil {
		return nil, err
	}
	return &tournamentResponse{Body: t}, nil
}

func (h *Handler) removeEntrant(ctx context.Context, in *tournamentPlayerRequest) (*tournamentResponse, error) {
	t, err := h.Tournaments.Remove(ctx, tournaments.AsPlayer(caller.FromContext(ctx).Subject), in.Tournament, in.Body.Player)
	if err != nil {
		return nil, err
	}
	return &tournamentResponse{Body: t}, nil
}

func (h *Handler) inviteToTournament(ctx context.Context, in *tournamentInviteRequest) (*tournamentInviteResponse, error) {
	invited, err := h.Tournaments.Invite(ctx, caller.FromContext(ctx).Subject, in.Tournament, in.Body.Players)
	if err != nil {
		return nil, err
	}
	out := &tournamentInviteResponse{}
	out.Body.Invited = invited
	return out, nil
}

func (h *Handler) startTournament(ctx context.Context, in *tournamentPath) (*tournamentResponse, error) {
	t, err := h.Tournaments.Start(ctx, tournaments.AsPlayer(caller.FromContext(ctx).Subject), in.Tournament)
	if err != nil {
		return nil, err
	}
	return &tournamentResponse{Body: t}, nil
}

func (h *Handler) cancelTournament(ctx context.Context, in *tournamentPath) (*tournamentResponse, error) {
	t, err := h.Tournaments.Cancel(ctx, tournaments.AsPlayer(caller.FromContext(ctx).Subject), in.Tournament)
	if err != nil {
		return nil, err
	}
	return &tournamentResponse{Body: t}, nil
}

func (h *Handler) finishTournament(ctx context.Context, in *tournamentPath) (*tournamentResponse, error) {
	t, err := h.Tournaments.Finish(ctx, tournaments.AsPlayer(caller.FromContext(ctx).Subject), in.Tournament)
	if err != nil {
		return nil, err
	}
	return &tournamentResponse{Body: t}, nil
}

func (h *Handler) playMatch(ctx context.Context, in *tournamentMatchPath) (*roomResponse, error) {
	r, err := h.Tournaments.Play(ctx, caller.FromContext(ctx).Subject, in.Tournament, in.Match)
	if err != nil {
		return nil, err
	}
	return &roomResponse{Body: r}, nil
}

func (h *Handler) awardMatch(ctx context.Context, in *tournamentAwardRequest) (*tournamentResponse, error) {
	t, err := h.Tournaments.Award(ctx, tournaments.AsPlayer(caller.FromContext(ctx).Subject), in.Tournament, in.Match, in.Body.Player)
	if err != nil {
		return nil, err
	}
	return &tournamentResponse{Body: t}, nil
}
