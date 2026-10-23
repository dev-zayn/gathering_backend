package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/tournaments"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

// Tournaments, as the console sees them: every one, a player's too, one with
// its bracket or its table, the official ones it makes, and its hand in any
// of them — start, cancel, finish a league, decide a match.
// docs/TOURNAMENTS.md, "The console", is the contract.

type adminTournamentsRequest struct {
	Status string `query:"status" example:"live" doc:"live (the default: open and running), open, running, done, cancelled, or all (status: unknown)."`
	Game   string `query:"game" example:"fixture.xo" doc:"One game's tournaments."`
	Cursor string `query:"cursor" doc:"next_cursor from the page before; leave it out for the first page (cursor: invalid)."`
	Limit  int    `query:"limit" default:"50" doc:"1 to 100 (limit: 1_to_100)."`
}

type adminTournamentsResponse struct {
	Body *tournaments.TournamentPage
}

type adminTournamentPath struct {
	Tournament string `path:"tournament" maxLength:"24" example:"trn_7f3k9d2m8q1x4b6n"`
}

type adminTournamentResponse struct {
	Body *tournaments.Tournament
}

type adminTournamentCreateRequest struct {
	Body struct {
		Game     string     `json:"game" required:"true" maxLength:"64" example:"fixture.xo"`
		Name     string     `json:"name,omitempty" maxLength:"200" example:"بطولة الجمعة" doc:"1 to 40 characters (name: required, name: too_long)."`
		Format   string     `json:"format,omitempty" example:"knockout" doc:"knockout or league (format: unknown, format: needs_duels)."`
		BestOf   int        `json:"best_of,omitempty" example:"3" doc:"Knockout: 1, 3 or 5 (best_of: 1_3_or_5)."`
		Rounds   int        `json:"rounds,omitempty" example:"5" doc:"League: 1 to 20 (rounds: 1_to_20)."`
		Size     int        `json:"size,omitempty" example:"16" doc:"The most entrants (size: out_of_range)."`
		StartsAt *time.Time `json:"starts_at,omitempty" doc:"When the clock starts it, or calls it off short of entrants; in the future (starts_at: in_the_past). Without one, an admin starts it."`
	}
}

type adminTournamentAwardRequest struct {
	Tournament string `path:"tournament" maxLength:"24" example:"trn_7f3k9d2m8q1x4b6n"`
	Match      string `path:"match" maxLength:"24" example:"trm_7f3k9d2m8q1x4b6n"`
	Body       struct {
		Player string `json:"player" required:"true" maxLength:"24" example:"ply_7f3k9d2m8q1x4b6n" doc:"One of the match's two players (player: not_in_match)."`
	}
}

func (h *Handler) registerTournaments(api huma.API) {
	admin := map[string]any{authn.ScopeKey: caller.ScopeAdmin}
	write := map[string]any{authn.ScopeKey: caller.ScopeAdmin, authn.RoleKey: string(enum.AdminRoleAdmin)}

	huma.Register(api, huma.Operation{
		OperationID: "admin-tournaments", Method: http.MethodGet, Path: "/v1/admin/tournaments",
		Summary: "Tournaments",
		Description: "Every tournament, a player's and the official ones, newest first, a page at a time: " +
			"hand next_cursor back as cursor until it is null.",
		Tags: []string{"admin"}, Metadata: admin,
	}, h.tournaments)

	huma.Register(api, huma.Operation{
		OperationID: "admin-tournament", Method: http.MethodGet, Path: "/v1/admin/tournaments/{tournament}",
		Summary:     "One tournament",
		Description: "Its entrants, its matches with their tallies and open rooms, and its standings.",
		Tags:        []string{"admin"}, Metadata: admin,
	}, h.tournament)

	huma.Register(api, huma.Operation{
		OperationID: "admin-tournament-create", Method: http.MethodPost, Path: "/v1/admin/tournaments",
		Summary: "Make an official tournament",
		Description: "Public, listed for every player, with no organizer and no entrant yet. At starts_at the " +
			"clock starts it, or calls it off short of entrants. Admins only; written to the log.",
		Tags: []string{"admin"}, Metadata: write, DefaultStatus: http.StatusCreated,
	}, h.createTournament)

	huma.Register(api, huma.Operation{
		OperationID: "admin-tournament-start", Method: http.MethodPost, Path: "/v1/admin/tournaments/{tournament}/start",
		Summary: "Start a tournament: the draw",
		Description: "Any open tournament with enough entrants. tournament_started, tournament_closed, " +
			"too_few_entrants. Admins only; written to the log.",
		Tags: []string{"admin"}, Metadata: write,
	}, h.startTournament)

	huma.Register(api, huma.Operation{
		OperationID: "admin-tournament-cancel", Method: http.MethodPost, Path: "/v1/admin/tournaments/{tournament}/cancel",
		Summary: "Call a tournament off",
		Description: "Any open or running tournament, for good, the reason admin: every match room closes and " +
			"every entrant is told. tournament_closed. Admins only; written to the log.",
		Tags: []string{"admin"}, Metadata: write,
	}, h.cancelTournament)

	huma.Register(api, huma.Operation{
		OperationID: "admin-tournament-finish", Method: http.MethodPost, Path: "/v1/admin/tournaments/{tournament}/finish",
		Summary: "Finish a league now",
		Description: "Its table as it stands decides. tournament_not_running, tournament_closed, not_league, " +
			"nothing_played. Admins only; written to the log.",
		Tags: []string{"admin"}, Metadata: write,
	}, h.finishTournament)

	huma.Register(api, huma.Operation{
		OperationID: "admin-tournament-award", Method: http.MethodPost,
		Path:    "/v1/admin/tournaments/{tournament}/matches/{match}/award",
		Summary: "Decide a match",
		Description: "A ready knockout match decided for one of its two players, its room closed. " +
			"tournament_not_running, tournament_closed, not_knockout, match_waiting, match_decided, " +
			"player: not_in_match. Admins only; written to the log.",
		Tags: []string{"admin"}, Metadata: write,
	}, h.awardMatch)
}

func (h *Handler) tournaments(ctx context.Context, in *adminTournamentsRequest) (*adminTournamentsResponse, error) {
	page, err := h.Tournaments.ConsoleList(ctx, tournaments.ConsoleQuery{
		Status: in.Status, Game: in.Game, Cursor: in.Cursor, Limit: in.Limit,
	})
	if err != nil {
		return nil, err
	}
	return &adminTournamentsResponse{Body: page}, nil
}

func (h *Handler) tournament(ctx context.Context, in *adminTournamentPath) (*adminTournamentResponse, error) {
	t, err := h.Tournaments.Get(ctx, tournaments.AsAdmin(caller.FromContext(ctx).Subject), in.Tournament)
	if err != nil {
		return nil, err
	}
	return &adminTournamentResponse{Body: t}, nil
}

func (h *Handler) createTournament(ctx context.Context, in *adminTournamentCreateRequest) (*adminTournamentResponse, error) {
	t, err := h.Tournaments.CreateOfficial(ctx, caller.FromContext(ctx).Subject, tournaments.CreateRequest{
		Game: in.Body.Game, Name: in.Body.Name, Format: in.Body.Format, BestOf: in.Body.BestOf,
		Rounds: in.Body.Rounds, Size: in.Body.Size, StartsAt: in.Body.StartsAt,
	})
	if err != nil {
		return nil, err
	}
	return &adminTournamentResponse{Body: t}, nil
}

func (h *Handler) startTournament(ctx context.Context, in *adminTournamentPath) (*adminTournamentResponse, error) {
	t, err := h.Tournaments.Start(ctx, tournaments.AsAdmin(caller.FromContext(ctx).Subject), in.Tournament)
	if err != nil {
		return nil, err
	}
	return &adminTournamentResponse{Body: t}, nil
}

func (h *Handler) cancelTournament(ctx context.Context, in *adminTournamentPath) (*adminTournamentResponse, error) {
	t, err := h.Tournaments.Cancel(ctx, tournaments.AsAdmin(caller.FromContext(ctx).Subject), in.Tournament)
	if err != nil {
		return nil, err
	}
	return &adminTournamentResponse{Body: t}, nil
}

func (h *Handler) finishTournament(ctx context.Context, in *adminTournamentPath) (*adminTournamentResponse, error) {
	t, err := h.Tournaments.Finish(ctx, tournaments.AsAdmin(caller.FromContext(ctx).Subject), in.Tournament)
	if err != nil {
		return nil, err
	}
	return &adminTournamentResponse{Body: t}, nil
}

func (h *Handler) awardMatch(ctx context.Context, in *adminTournamentAwardRequest) (*adminTournamentResponse, error) {
	t, err := h.Tournaments.Award(ctx, tournaments.AsAdmin(caller.FromContext(ctx).Subject), in.Tournament, in.Match, in.Body.Player)
	if err != nil {
		return nil, err
	}
	return &adminTournamentResponse{Body: t}, nil
}
