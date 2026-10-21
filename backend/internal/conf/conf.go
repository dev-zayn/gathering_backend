// Package conf reads everything the binary needs from the environment. .env on a
// laptop, a secret store in production; the code cannot tell the difference.
package conf

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jamaa/gathering/backend/internal/data"
)

type Config struct {
	Addr    string
	Version string

	// Debug is a laptop. It lets the OTP go to the log instead of a phone, lets
	// an admin sign in without a provider (DevAdmin), and logs every SQL
	// statement. Never on in production.
	Debug bool

	Postgres data.Config
	RedisURL string

	// Logs is the same database again, on a pool of its own: the log tables grow
	// without limit, and a burst of writes to them must not take the connections
	// player traffic needs.
	Logs data.Config

	// CORSOrigins is every origin a browser may call this API from: the console
	// and the docs.
	CORSOrigins []string

	// SweepEvery is how often the api tidies its tables, ends rooms nobody is
	// in and re-reads the manifests that are due.
	SweepEvery time.Duration

	Files    FilesConfig
	Manifest ManifestConfig

	// AllowPrivateHosts lets us fetch a game's manifest from a private address.
	// Only a laptop wants it: the fixtures run there.
	AllowPrivateHosts bool

	// CodeRetention is how long housekeeping keeps a spent launch code.
	CodeRetention time.Duration

	// PublicURL is the address the outside reaches us on, not the one this
	// process listens on. Discovery has to name it.
	PublicURL string

	// DocsURL is the developer documentation, on its own address.
	DocsURL string

	// Links is where invite links live and how the phones claim them.
	Links LinksConfig

	SSO   SSOConfig
	Token TokenConfig
	OTP   OTPConfig
	Push  PushConfig
	Relay RelayConfig
	Rooms RoomsConfig
}

// SSOConfig is the OIDC provider that says who an admin is. Any provider that
// puts the person's roles in a claim works; Service is the prefix our roles
// carry there ("gathering.admin"), so one provider can serve several services.
type SSOConfig struct {
	Issuer     string
	ClientID   string
	Audience   string
	RolesClaim string
	Service    string
	// Dial is host:port to reach the issuer from inside Docker, where the
	// issuer's localhost is not the provider. Empty everywhere else.
	Dial string
	// DevAdmin lets POST /v1/admin/session/dev mint an admin with no provider
	// at all. Honoured only with Debug.
	DevAdmin bool
}

// Configured says whether there is a provider to verify admins against.
func (s SSOConfig) Configured() bool {
	return s.Issuer != "" && s.ClientID != "" && s.Audience != ""
}

// PushConfig is Firebase Cloud Messaging. Without it a push is a log line
// instead: the inbox still fills, and nothing else is held up by the phones.
type PushConfig struct {
	ProjectID string
	// Credentials is the service account's JSON key, decoded.
	Credentials []byte
	Timeout     time.Duration
	// PerGameHourly is how many pushes one game may send one player in an hour.
	PerGameHourly int
}

// Configured says whether there is a Firebase project to push through.
func (p PushConfig) Configured() bool { return p.ProjectID != "" && len(p.Credentials) > 0 }

type TokenConfig struct {
	// PrivateKey signs; its public half verifies and is published as the JWKS.
	PrivateKey string
	Issuer     string

	OTPTTL     time.Duration
	SignupTTL  time.Duration
	SessionTTL time.Duration
	RefreshTTL time.Duration

	// AdminTTL is the longest a console session lasts however busy; AdminIdle
	// is how long it survives unused.
	AdminTTL  time.Duration
	AdminIdle time.Duration

	// InsecureCookie drops Secure from the console's cookie. Only a laptop on
	// Safari wants it: Safari will not keep a Secure cookie from http://localhost.
	InsecureCookie bool

	// CodeTTL is the one-time launch code: it only has to survive a redirect.
	// GameTTL is the credential a game's backend keeps, renewed server-side.
	// RoomTTL is a room ticket: what a game's backend verifies a player with.
	CodeTTL time.Duration
	GameTTL time.Duration
	RoomTTL time.Duration
}

// OTPConfig is how a code reaches a phone. The sender is a webhook, so any SMS
// or WhatsApp gateway can be bridged with a few lines; with Debug and no
// webhook the code goes to the log.
type OTPConfig struct {
	WebhookURL   string
	WebhookToken string
	Timeout      time.Duration

	// FixedCode makes every code this one, so an automated sweep can sign in
	// without reading the log. Only with Debug.
	FixedCode string

	// CodeTTL is how long a code stays usable, Attempts how many wrong guesses
	// kill it.
	CodeTTL  time.Duration
	Attempts int

	// Ceiling is what the whole deployment may send in a minute. The two below
	// are per number and cannot see a caller rotating through them.
	Ceiling int

	// Cooldown is the wait between two codes to one number; Hourly is the
	// ceiling for one number in an hour; Resends how many times one ticket may
	// ask again.
	Cooldown time.Duration
	Hourly   int
	Resends  int
}

// Configured says whether there is a real gateway to send through.
func (o OTPConfig) Configured() bool { return o.WebhookURL != "" }

// FilesConfig is where pictures live: a private S3 bucket, every link signed
// and expiring — or, on a laptop, a directory (Dir), served by the api itself.
type FilesConfig struct {
	Bucket string
	Region string
	Dir    string

	// AvatarTTL and IconTTL are how long a signed link lives. Both are drawn
	// on every screen, so neither is short.
	AvatarTTL time.Duration
	IconTTL   time.Duration
}

// Configured says whether there is somewhere to put a picture.
func (f FilesConfig) Configured() bool { return f.Bucket != "" || f.Dir != "" }

// ManifestConfig is how a game's description is kept current: re-read rather
// than announced.
type ManifestConfig struct {
	MaxAge time.Duration
	Batch  int
}

// LinksConfig is the invite link, https://<host>/r/<code>, and the two files
// the phones read to claim that host as theirs.
type LinksConfig struct {
	PublicURL      string
	AppBundleID    string
	AppLinksSHA256 []string
	AppleTeamID    string
	StoreURLIOS    string
	StoreURLAndrd  string
}

// RelayConfig bounds what one socket may do. The relay never reads inside a
// frame, so these are the only rules it has.
type RelayConfig struct {
	MaxFrame   int
	Rate       int
	MaxMembers int
	Presence   time.Duration
}

// RoomsConfig is how long a room may sit empty, and how long a game may run.
type RoomsConfig struct {
	LobbyIdle time.Duration
	MaxPlay   time.Duration
	Alphabet  string
}

// Load fails loudly and immediately: a binary missing a signing key should not
// start and then break on the first request.
func Load() (Config, error) {
	var missing []string
	req := func(key string) string {
		v := os.Getenv(key)
		if v == "" {
			missing = append(missing, key)
		}
		return v
	}

	c := Config{
		Addr:    opt("ADDR", ":8080"),
		Version: opt("VERSION", "dev"),
		Debug:   opt("DEBUG", "") == "true",

		Postgres: data.Config{
			DSN:          req("DATABASE_URL"),
			MaxOpenConns: optInt("DB_MAX_OPEN_CONNS", 20),
			MaxIdleConns: optInt("DB_MAX_IDLE_CONNS", 5),
		},
		Logs: data.Config{
			DSN:          os.Getenv("DATABASE_URL"),
			MaxOpenConns: optInt("LOGS_DB_MAX_OPEN_CONNS", 5),
			MaxIdleConns: optInt("LOGS_DB_MAX_IDLE_CONNS", 2),
		},
		RedisURL:    req("REDIS_URL"),
		SweepEvery:  optDur("SWEEP_INTERVAL", time.Minute),
		CORSOrigins: list("CORS_ORIGINS"),

		Files: FilesConfig{
			Bucket:    os.Getenv("S3_BUCKET"),
			Region:    opt("S3_REGION", "me-south-1"),
			Dir:       os.Getenv("FILES_DIR"),
			AvatarTTL: optDur("S3_AVATAR_URL_TTL", time.Hour),
			IconTTL:   optDur("S3_ICON_URL_TTL", time.Hour),
		},

		Manifest: ManifestConfig{
			MaxAge: optDur("MANIFEST_MAX_AGE", 24*time.Hour),
			Batch:  optInt("MANIFEST_BATCH", 20),
		},

		SSO: SSOConfig{
			Issuer:     os.Getenv("SSO_ISSUER"),
			ClientID:   os.Getenv("SSO_CLIENT_ID"),
			Audience:   os.Getenv("SSO_AUDIENCE"),
			RolesClaim: opt("SSO_ROLES_CLAIM", "roles"),
			Service:    opt("SSO_SERVICE", "gathering"),
			Dial:       os.Getenv("SSO_DIAL"),
			DevAdmin:   opt("DEV_ADMIN", "") == "true",
		},
		AllowPrivateHosts: opt("ALLOW_PRIVATE_HOSTS", "") == "true",

		// A code lives one minute, so a day is generous: long enough that a
		// failure is still on record while somebody reads the logs.
		CodeRetention: optDur("CODE_RETENTION", 24*time.Hour),
		PublicURL:     opt("API_PUBLIC_URL", "http://localhost:8080"),
		DocsURL:       opt("DOCS_URL", "http://localhost:8092"),

		Links: LinksConfig{
			PublicURL:      opt("LINK_PUBLIC_URL", "http://localhost:8080"),
			AppBundleID:    opt("APP_BUNDLE_ID", "app.gathering.jamaa"),
			AppLinksSHA256: list("APP_LINKS_SHA256"),
			AppleTeamID:    os.Getenv("APPLE_TEAM_ID"),
			StoreURLIOS:    os.Getenv("STORE_URL_IOS"),
			StoreURLAndrd:  os.Getenv("STORE_URL_ANDROID"),
		},

		Token: TokenConfig{
			PrivateKey:     req("JWT_PRIVATE_KEY"),
			Issuer:         opt("JWT_ISSUER", "http://localhost:8080"),
			OTPTTL:         optDur("TOKEN_OTP_TTL", 5*time.Minute),
			SignupTTL:      optDur("TOKEN_SIGNUP_TTL", 15*time.Minute),
			SessionTTL:     optDur("TOKEN_SESSION_TTL", time.Hour),
			RefreshTTL:     optDur("TOKEN_REFRESH_TTL", 90*24*time.Hour),
			AdminTTL:       optDur("TOKEN_ADMIN_TTL", 12*time.Hour),
			AdminIdle:      optDur("TOKEN_ADMIN_IDLE", 10*time.Minute),
			InsecureCookie: os.Getenv("INSECURE_COOKIE") == "true",
			CodeTTL:        optDur("TOKEN_CODE_TTL", time.Minute),
			GameTTL:        optDur("TOKEN_GAME_TTL", time.Hour),
			RoomTTL:        optDur("TOKEN_ROOM_TTL", 10*time.Minute),
		},

		OTP: OTPConfig{
			WebhookURL:   os.Getenv("OTP_WEBHOOK_URL"),
			WebhookToken: os.Getenv("OTP_WEBHOOK_TOKEN"),
			FixedCode:    os.Getenv("OTP_FIXED_CODE"),
			Timeout:      optDur("OTP_TIMEOUT", 30*time.Second),
			CodeTTL:      optDur("OTP_CODE_TTL", 5*time.Minute),
			Attempts:     optInt("OTP_ATTEMPTS", 5),
			Ceiling:      optInt("OTP_CEILING", 120),
			Cooldown:     optDur("OTP_COOLDOWN", time.Minute),
			Hourly:       optInt("OTP_HOURLY", 5),
			Resends:      optInt("OTP_RESENDS", 2),
		},

		Relay: RelayConfig{
			MaxFrame:   optInt("RELAY_MAX_FRAME", 16<<10),
			Rate:       optInt("RELAY_RATE", 30),
			MaxMembers: optInt("RELAY_MAX_MEMBERS", 64),
			Presence:   optDur("PRESENCE_TTL", time.Minute),
		},
		Rooms: RoomsConfig{
			LobbyIdle: optDur("ROOM_LOBBY_IDLE", 10*time.Minute),
			MaxPlay:   optDur("ROOM_MAX_PLAY", 2*time.Hour),
			Alphabet:  opt("ROOM_CODE_ALPHABET", "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"),
		},
	}

	c.Push = PushConfig{
		ProjectID:     os.Getenv("FCM_PROJECT_ID"),
		Timeout:       optDur("FCM_TIMEOUT", 10*time.Second),
		PerGameHourly: optInt("PUSH_PER_GAME_HOURLY", 20),
	}
	if raw := strings.TrimSpace(os.Getenv("FCM_CREDENTIALS")); raw != "" {
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return Config{}, fmt.Errorf("config: FCM_CREDENTIALS is not base64: %w", err)
		}
		c.Push.Credentials = key
	}

	// Each of these has a laptop stand-in behind Debug and nothing else: no
	// gateway and Debug off means nobody can sign in, so it refuses to start.
	if !c.OTP.Configured() && !c.Debug {
		missing = append(missing, "OTP_WEBHOOK_URL")
	}
	if !c.SSO.Configured() && (!c.Debug || !c.SSO.DevAdmin) {
		missing = append(missing, "SSO_ISSUER, SSO_CLIENT_ID, SSO_AUDIENCE (or DEBUG=true with DEV_ADMIN=true)")
	}
	if !c.Files.Configured() {
		missing = append(missing, "S3_BUCKET (or FILES_DIR)")
	}
	if c.Files.Dir != "" && !c.Debug {
		return Config{}, fmt.Errorf("config: FILES_DIR is a laptop stand-in and needs DEBUG=true")
	}
	if c.SSO.DevAdmin && !c.Debug {
		return Config{}, fmt.Errorf("config: DEV_ADMIN needs DEBUG=true")
	}
	if c.OTP.FixedCode != "" && !c.Debug {
		return Config{}, fmt.Errorf("config: OTP_FIXED_CODE needs DEBUG=true")
	}
	if len(c.Rooms.Alphabet) < 16 {
		return Config{}, fmt.Errorf("config: ROOM_CODE_ALPHABET needs at least 16 symbols")
	}

	if len(missing) > 0 {
		return Config{}, fmt.Errorf("config: missing %s", strings.Join(missing, ", "))
	}
	return c, nil
}

// list reads a comma-separated variable, ignoring blanks.
func list(key string) []string {
	var out []string
	for _, p := range strings.Split(os.Getenv(key), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func opt(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func optInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func optDur(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return v
	}
	return def
}
