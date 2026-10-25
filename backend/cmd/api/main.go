// Command api runs Gathering: sign-in, players and friends, the game registry
// and library, rooms and their relay, tournaments, and the inbox. The wiring is the graph
// main builds below.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/joho/godotenv"
	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz"
	"github.com/jamaa/gathering/backend/internal/biz/audit"
	"github.com/jamaa/gathering/backend/internal/biz/friends"
	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/biz/guests"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/biz/inbox"
	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/biz/push"
	"github.com/jamaa/gathering/backend/internal/biz/relay"
	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/biz/tournaments"
	"github.com/jamaa/gathering/backend/internal/conf"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/migrations"
	apphttp "github.com/jamaa/gathering/backend/internal/http"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/http/guest"
	"github.com/jamaa/gathering/backend/internal/pkg/fcm"
	"github.com/jamaa/gathering/backend/internal/pkg/files"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
	"github.com/jamaa/gathering/backend/internal/pkg/otp"
	"github.com/jamaa/gathering/backend/internal/pkg/sso"
	"github.com/jamaa/gathering/backend/internal/pkg/token"
)

// HandleCooldown is how long a changed @name is held before it may change
// again. A week: long enough that friends find the name they were told.
const HandleCooldown = 7 * 24 * time.Hour

func main() {
	if apphttp.Spec(os.Args, "gathering", func(a huma.API) { apphttp.Mount(a, apphttp.Deps{}) }) {
		return
	}

	// .env locally, the task definition in production. Before anything reads a
	// variable, the logger included.
	_ = godotenv.Load()

	log := applog.New("gathering")

	cfg, err := conf.Load()
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pg, err := data.Open(data.Config{
		DSN:          cfg.Postgres.DSN,
		MaxOpenConns: cfg.Postgres.MaxOpenConns,
		MaxIdleConns: cfg.Postgres.MaxIdleConns,
		Debug:        cfg.Debug,
	})
	if err != nil {
		log.Fatal("postgres", "err", err)
	}

	rdb, err := data.OpenRedis(ctx, cfg.RedisURL)
	if err != nil {
		log.Fatal("redis", "err", err)
	}
	defer func() { _ = rdb.Close() }()

	// The same database on a pool of its own, so the log cannot starve players
	// of connections. Its tables come with the migrations below.
	logs, err := data.Open(cfg.Logs)
	if err != nil {
		log.Fatal("logs", "err", err)
	}

	sqlDB, err := pg.DB()
	if err != nil {
		log.Fatal("postgres", "err", err)
	}
	// The binary carries its own schema, so starting a new environment is
	// starting the service.
	if err := migrations.Up(ctx, sqlDB); err != nil {
		log.Fatal("migrations", "err", err)
	}
	log.Info("schema up to date")

	// Where the pictures go: the bucket, or on a laptop a directory this
	// process serves itself.
	bucket, localFiles, err := openFiles(ctx, cfg)
	if err != nil {
		log.Fatal("files", "err", err)
	}
	if localFiles != nil {
		log.Warn("FILES_DIR is set: pictures are served from a directory, unsigned", "dir", cfg.Files.Dir)
	}

	signer, err := token.NewSigner(cfg.Token.PrivateKey, cfg.Token.Issuer)
	if err != nil {
		log.Fatal("jwt keys", "err", err)
	}
	minter := identity.NewMinter(signer)

	authAudit := audit.New(logs, audit.AreaAuth)
	playersAudit := audit.New(logs, audit.AreaPlayers)
	gamesAudit := audit.New(logs, audit.AreaGames)
	roomsAudit := audit.New(logs, audit.AreaRooms)
	tournamentsAudit := audit.New(logs, audit.AreaTournaments)
	adminAudit := audit.New(logs, audit.AreaAdmin)

	// Codes reach a phone through a webhook to whatever gateway is wired; on a
	// laptop they go to this log.
	var sender identity.OTPSender = otp.NewWebhook(cfg.OTP.WebhookURL, cfg.OTP.WebhookToken, cfg.OTP.Timeout)
	if !cfg.OTP.Configured() {
		log.Warn("no OTP gateway configured — codes will be written to this log, not sent")
		sender = otp.LogSender{}
	}

	// The stores. Each is one table, and every rule below shares the one copy.
	playerStore := identity.NewPlayerStore(pg)
	sessions := identity.NewSessionStore(pg)
	refreshTokens := identity.NewRefreshStore(pg)
	admins := identity.NewAdminStore(pg)
	devices := identity.NewDevices(pg)
	codes := identity.NewOTPStore(rdb)
	whitelist := identity.NewWhitelist(rdb)
	launchCodes := identity.NewCodeStore(pg)
	gameTokens := identity.NewGameTokenStore(pg)

	signIn := &identity.SignIn{
		Codes:   codes,
		Sender:  sender,
		Minter:  minter,
		Limiter: identity.NewLimiter(rdb),
		Policy: identity.SignInPolicy{
			CodeTTL:  cfg.OTP.CodeTTL,
			TokenTTL: cfg.Token.OTPTTL,
			Ceiling:  cfg.OTP.Ceiling,
			Cooldown: cfg.OTP.Cooldown,
			Hourly:   cfg.OTP.Hourly,
			Resends:  cfg.OTP.Resends,
		},
		Fixed: cfg.OTP.FixedCode,
	}
	if cfg.OTP.FixedCode != "" {
		log.Warn("OTP_FIXED_CODE is set: every code is the same")
	}

	issuer := &identity.Issuer{
		Sessions:   sessions,
		Tokens:     refreshTokens,
		Minter:     minter,
		Whitelist:  whitelist,
		SessionTTL: cfg.Token.SessionTTL,
		RefreshTTL: cfg.Token.RefreshTTL,
	}

	verify := &identity.Verify{
		Audit:   authAudit,
		Codes:   codes,
		Players: playerStore,
		Minter:  minter,
		Issuer:  issuer,
		Policy: identity.VerifyPolicy{
			MaxAttempts: cfg.OTP.Attempts,
			SignupTTL:   cfg.Token.SignupTTL,
		},
	}

	signup := &biz.Signup{
		Tx:      data.NewTx(pg),
		Players: playerStore,
		Issuer:  issuer,
		Audit:   authAudit,
	}

	refresh := &identity.Refresh{
		Tokens:    refreshTokens,
		Sessions:  sessions,
		Players:   playerStore,
		Whitelist: whitelist,
		Issuer:    issuer,
	}

	// The provider says who an admin is, once; the console then holds a
	// session of ours, which signing out and sitting idle both end. A laptop
	// may skip the provider (DEV_ADMIN), and then the provider is not even
	// asked for.
	var who identity.SSO = unavailableSSO{}
	if cfg.SSO.Configured() {
		v, err := sso.New(sso.Config{
			Issuer:     cfg.SSO.Issuer,
			ClientID:   cfg.SSO.ClientID,
			Audience:   cfg.SSO.Audience,
			RolesClaim: cfg.SSO.RolesClaim,
			Service:    cfg.SSO.Service,
			Dial:       cfg.SSO.Dial,
		})
		if err != nil {
			log.Fatal("sso", "err", err)
		}
		who = ssoBridge{v: v}
	} else {
		log.Warn("no identity provider configured — only the dev sign-in opens the console")
	}

	console := &identity.Console{
		SSO:        who,
		Admins:     admins,
		Minter:     minter,
		Whitelist:  whitelist,
		Audit:      adminAudit,
		TTL:        cfg.Token.AdminTTL,
		Idle:       cfg.Token.AdminIdle,
		DevAllowed: cfg.Debug && cfg.SSO.DevAdmin,
		Password:   []byte(cfg.SSO.PasswordHash),
		Limiter:    identity.NewLimiter(rdb),
	}
	if console.DevAllowed {
		log.Warn("DEV_ADMIN is on: anybody who reaches the console may sign in as an admin")
	}

	// Asked on every guarded request, which makes it the busiest thing here.
	identities := &identity.Identities{
		Signer: signer, Whitelist: whitelist, AdminIdle: cfg.Token.AdminIdle,
	}

	presence := players.NewPresence(rdb, cfg.Relay.Presence)
	profiles := &players.Profiles{
		Players:        playerStore,
		Audit:          playersAudit,
		HandleCooldown: HandleCooldown,
	}

	// The games. Both the catalogue and the registry revoke: a player
	// removing a game and an admin suspending one are the same act.
	revoker := &games.Revoker{Tokens: gameTokens, Whitelist: whitelist}
	catalogue := games.NewCatalogue(pg, revoker, gamesAudit)
	registry := games.NewRegistry(pg, games.NewHTTPFetcher(cfg.AllowPrivateHosts), gamesAudit, revoker)
	registry.Files = bucket
	registry.MaxAge = cfg.Manifest.MaxAge
	registry.Batch = cfg.Manifest.Batch
	if cfg.AllowPrivateHosts {
		log.Warn("ALLOW_PRIVATE_HOSTS is on: a manifest may be fetched from a private address")
	}

	// The inbox, and the friends: a request and an accept raise their
	// notifications in the same transaction as the answer itself. The
	// console's announcements go on the record with the players. Every
	// notification, once committed, goes on to its recipient's phones.
	pusher, err := openPush(cfg.Push, pg, devices, log)
	if err != nil {
		log.Fatal("push", "err", err)
	}
	notifications := inbox.New(pg)
	notifications.Files, notifications.AvatarTTL = bucket, cfg.Files.AvatarTTL
	notifications.Audit = playersAudit
	notifications.Pusher = pusher
	friendRules := friends.New(pg, playerStore, notifications)
	friendRules.Presence = presence
	friendRules.Files, friendRules.AvatarTTL = bucket, cfg.Files.AvatarTTL

	// The rooms, and the relay that carries their frames. Each needs the
	// other: the rules publish through the hub, and the hub asks the rules
	// who may say what. The hub is the process's one subscriber to Redis.
	roomRules := rooms.New(pg, rooms.Config{
		Alphabet:   cfg.Rooms.Alphabet,
		MaxMembers: cfg.Relay.MaxMembers,
		LobbyIdle:  cfg.Rooms.LobbyIdle,
		MaxPlay:    cfg.Rooms.MaxPlay,
		TicketTTL:  cfg.Token.RoomTTL,
		LinkURL:    cfg.Links.PublicURL,
		AvatarTTL:  cfg.Files.AvatarTTL,
	})
	roomRules.Presence = presence
	roomRules.Minter = minter
	roomRules.Files = bucket
	roomRules.Friends = friendRules
	roomRules.Inbox = notifications
	roomRules.Audit = roomsAudit
	relayCfg := relay.DefaultConfig()
	relayCfg.MaxFrame, relayCfg.Rate = cfg.Relay.MaxFrame, cfg.Relay.Rate
	hub := relay.NewHub(rdb, roomRules, presence, relayCfg, log)
	roomRules.Publisher = hub

	// The tournaments, whose matches are played in rooms: the rules ask the
	// tournaments who may sit in a match room and what its rounds decide,
	// and the tournaments ask the rooms for a match's room and to close it.
	tournamentRules := tournaments.New(pg, tournaments.Config{
		Alphabet:    cfg.Rooms.Alphabet,
		MaxEntrants: cfg.Tournaments.MaxEntrants,
		MaxMembers:  cfg.Relay.MaxMembers,
		OpenFor:     cfg.Tournaments.OpenFor,
		LinkURL:     cfg.Links.PublicURL,
		AvatarTTL:   cfg.Files.AvatarTTL,
	})
	tournamentRules.Rooms = roomRules
	tournamentRules.Friends = friendRules
	tournamentRules.Inbox = notifications
	tournamentRules.Files = bucket
	tournamentRules.Audit = tournamentsAudit
	roomRules.Matches = tournamentRules

	launch := &guests.Launch{
		Codes:     launchCodes,
		Tokens:    gameTokens,
		Players:   playerStore,
		Games:     catalogue,
		Minter:    minter,
		Whitelist: whitelist,
		Rooms:     roomRules,
		CodeTTL:   cfg.Token.CodeTTL,
		TokenTTL:  cfg.Token.GameTTL,
	}

	bridge := &guests.Bridge{
		Players:   playerStore,
		Games:     catalogue,
		Files:     bucket,
		AvatarTTL: cfg.Files.AvatarTTL,
		Rooms:     roomRules,
		Inbox:     notifications,
		Limiter:   identity.NewLimiter(rdb),
	}

	maintenance := &biz.Maintenance{
		Devices:       devices,
		Codes:         launchCodes,
		CodeRetention: cfg.CodeRetention,
		Notifications: notifications,
	}

	r, humaAPI := apphttp.New("gathering", cfg.Version, log, cfg.CORSOrigins...)
	apphttp.Mount(humaAPI, apphttp.Deps{
		Resolve: identities.Resolve,
		// The console's session, in a cookie only its own origins may send.
		AdminCookie: authn.NewCookie(!cfg.Token.InsecureCookie, cfg.CORSOrigins),

		SignIn:  signIn,
		Verify:  verify,
		Refresh: refresh,
		Signup:  signup,
		Devices: devices,

		Profiles: profiles,
		Presence: presence,
		Players:  playerStore,

		Games:    catalogue,
		Registry: registry,
		Launch:   launch,
		Bridge:   bridge,

		Rooms:       roomRules,
		Relay:       hub,
		Tournaments: tournamentRules,
		AppLinks: guest.AppLinks{
			AndroidPackage: cfg.Links.AndroidPackage,
			SHA256:         cfg.Links.AppLinksSHA256,
			AppleTeamID:    cfg.Links.AppleTeamID,
			IOSBundle:      cfg.Links.IOSBundle,
			AppStoreURL:    cfg.Links.AppStoreURL,
			PlayStoreURL:   cfg.Links.PlayStoreURL,
		},

		Friends: friendRules,
		Inbox:   notifications,

		Files:      bucket,
		LocalFiles: localFiles,

		Console:   console,
		Admins:    admins,
		Dashboard: biz.NewDashboard(pg, presence),
		Audit:     audit.NewReader(logs),

		Signer:    signer,
		PublicURL: cfg.PublicURL,
		DocsURL:   cfg.DocsURL,

		Ready: map[string]apphttp.Check{
			"postgres": sqlDB.PingContext,
			"logs": func(ctx context.Context) error {
				db, err := logs.DB()
				if err != nil {
					return err
				}
				return db.PingContext(ctx)
			},
			"redis": func(ctx context.Context) error { return rdb.Ping(ctx).Err() },
		},
	})

	go clock(ctx, cfg.SweepEvery, []Job{
		{Name: "sweep", Run: func(ctx context.Context) (string, error) {
			s, err := maintenance.Sweep(ctx)
			if err != nil || !s.Any() {
				return "", err
			}
			return fmt.Sprintf("devices=%d codes=%d notifications=%d", s.Devices, s.Codes, s.Notifications), nil
		}},
		{Name: "rooms", Run: func(ctx context.Context) (string, error) {
			s, err := roomRules.Sweep(ctx, time.Now())
			if !s.Any() {
				return "", err
			}
			return fmt.Sprintf("idle=%d timeout=%d deleted=%d", s.Idle, s.Timeout, s.Deleted), err
		}},
		{Name: "tournaments", Run: func(ctx context.Context) (string, error) {
			s, err := tournamentRules.Sweep(ctx, time.Now())
			if !s.Any() {
				return "", err
			}
			return fmt.Sprintf("started=%d cancelled=%d", s.Started, s.Cancelled), err
		}},
		{Name: "manifests", Run: func(ctx context.Context) (string, error) {
			read, err := registry.Reread(ctx)
			if err != nil || read.Read+read.Failed == 0 {
				return "", err
			}
			return fmt.Sprintf("read=%d changed=%d failed=%d", read.Read, read.Changed, read.Failed), nil
		}},
	}, log)

	err = apphttp.Serve(ctx, cfg.Addr, r, log)

	// The server stops answering requests but never tracked the sockets: it
	// handed them over. They close with 1001, and the app reconnects to
	// another replica with after.
	hctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	hub.Close(hctx)
	cancel()

	// No request is left to raise anything: the pushes queued go out, for a
	// few seconds at most, and the rest are abandoned. The inbox has them.
	pctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	pusher.Close(pctx)
	cancel()

	if err != nil {
		log.Fatal("serve", "err", err)
	}
	log.Info("stopped")
}

// openFiles is where the pictures go: the private bucket in production, or
// on a laptop a directory this process serves under /files/. The handler is
// nil for the bucket, which signs its own links.
func openFiles(ctx context.Context, cfg conf.Config) (files.Files, http.Handler, error) {
	if cfg.Files.Bucket != "" {
		s, err := files.NewS3(ctx, files.S3Config{
			Bucket: cfg.Files.Bucket,
			Region: cfg.Files.Region,
		})
		return s, nil, err
	}
	local, err := files.NewLocal(cfg.Files.Dir, cfg.PublicURL)
	if err != nil {
		return nil, nil, err
	}
	return local, local.Handler(), nil
}

// openPush is the pool that carries notifications to the phones: through
// Firebase with the service account's key, or with no key, a "would push"
// line in this log for each notification.
func openPush(cfg conf.PushConfig, db *gorm.DB, devices *identity.Devices, log *applog.Logger) (*push.Service, error) {
	if !cfg.Configured() {
		log.Warn("no Firebase key configured (FCM_PROJECT_ID, FCM_CREDENTIALS_FILE) — each push is a line in this log, nothing is sent")
		return push.New(db, nil, devices, log, push.Options{}), nil
	}
	key, err := os.ReadFile(cfg.CredentialsFile)
	if err != nil {
		return nil, err
	}
	client, err := fcm.New(fcm.Config{
		ProjectID: cfg.ProjectID, Credentials: key, Endpoint: cfg.Endpoint, Timeout: cfg.Timeout,
	})
	if err != nil {
		return nil, err
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = fcm.DefaultEndpoint
	}
	log.Info("pushing through Firebase", "project", cfg.ProjectID, "endpoint", endpoint)
	return push.New(db, client, devices, log, push.Options{}), nil
}

// ssoBridge is the provider as identity sees it: the verifier's answer, with
// its failures sorted into the two identity tells apart.
type ssoBridge struct{ v *sso.Verifier }

func (b ssoBridge) Verify(ctx context.Context, raw string) (*identity.Person, error) {
	id, err := b.v.Verify(ctx, raw)
	if err != nil {
		if _, unavailable := b.v.Classify(err); unavailable {
			return nil, fmt.Errorf("%w: %v", identity.ErrSSOUnavailable, err)
		}
		return nil, fmt.Errorf("%w: %v", identity.ErrSSOInvalid, err)
	}
	return &identity.Person{Subject: id.Subject, Name: id.Name, Email: id.Email, Roles: id.Roles}, nil
}

// unavailableSSO is a deployment with no provider: every sign-in through one
// is refused as unavailable, and only the dev sign-in opens the console.
type unavailableSSO struct{}

func (unavailableSSO) Verify(context.Context, string) (*identity.Person, error) {
	return nil, fmt.Errorf("%w: no identity provider is configured", identity.ErrSSOUnavailable)
}
