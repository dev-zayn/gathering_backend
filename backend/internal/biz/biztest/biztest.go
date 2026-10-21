// Package biztest is what the tests under biz/ share: a real database, and
// stand-ins for the systems that are not ours. Only _test.go files import it.
package biztest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/migrations"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
	"github.com/jamaa/gathering/backend/internal/pkg/token"
)

// DB is a real database, one per package so the test binaries do not truncate
// each other. Without TEST_DATABASE_URL these skip.
func DB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn, err := data.ForTest("gathering_" + callerPackage(t))
	if err != nil {
		t.Fatal(err)
	}
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	pg, err := data.Open(data.Config{DSN: dsn, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := pg.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(context.Background(), sqlDB); err != nil {
		t.Fatal(err)
	}
	if err := truncate(pg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return pg
}

// vocabulary is what the migrations seed. Truncating it reads as broken data,
// not an empty one.
var vocabulary = map[string]bool{"scopes": true}

// truncate empties every table but the vocabulary, read from the catalogue
// rather than listed: each milestone brings tables of its own, and a list
// here would have to know them all before they exist.
func truncate(pg *gorm.DB) error {
	var rows []struct{ Schema, Name string }
	err := pg.Raw(`SELECT table_schema AS schema, table_name AS name FROM information_schema.tables
	               WHERE table_schema IN ('public', 'logs') AND table_type = 'BASE TABLE'`).Scan(&rows).Error
	if err != nil {
		return err
	}

	var tables []string
	for _, r := range rows {
		if r.Name == "goose_db_version" || vocabulary[r.Name] {
			continue
		}
		tables = append(tables, `"`+r.Schema+`"."`+r.Name+`"`)
	}
	if len(tables) == 0 {
		return nil
	}
	return pg.Exec("TRUNCATE " + strings.Join(tables, ", ") + " CASCADE").Error
}

// callerPackage is the directory the test lives in. Taken from the call site so
// a new package cannot forget one, or two pass the same one by accident.
func callerPackage(t *testing.T) string {
	t.Helper()

	// Three frames: this, DB, and the test that called it.
	_, file, _, ok := runtime.Caller(2)
	if !ok {
		t.Fatal("cannot tell which package is asking for a database")
	}
	return filepath.Base(filepath.Dir(file))
}

// Issuer is the iss every test token carries.
const Issuer = "https://api.gathering.test"

// Signer signs with a key made for this test alone, so no key material lives
// in the repository.
func Signer(t *testing.T) *token.Signer {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	s, err := token.NewSigner(string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), Issuer)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The seeds below use plain SQL rather than the stores that own these rows:
// those packages' tests import this one, so importing them back is a cycle.

var seeded atomic.Int64

// SeedPlayer writes one player. The handle is the latin of the nickname, or
// "player", and a number no other seed in this process gets, so two seeds
// never collide. An empty phone is a player with none.
func SeedPlayer(t *testing.T, pg *gorm.DB, phone, nickname string) *model.Player {
	t.Helper()

	now := data.Now()
	p := &model.Player{
		ID: ids.New(ids.Player), Nickname: nickname, Handle: handleFor(nickname),
		Language: "ar", CreatedAt: now, UpdatedAt: now,
	}
	if phone != "" {
		p.Phone = &phone
	}
	err := pg.Exec(`INSERT INTO players (id, phone, nickname, handle, language, created_at, updated_at)
	                VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Phone, p.Nickname, p.Handle, string(p.Language), now, now).Error
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func handleFor(nickname string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(nickname) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	stem := b.String()
	if stem == "" {
		stem = "player"
	}
	if len(stem) > 12 {
		stem = stem[:12]
	}
	return fmt.Sprintf("%s_%04d", stem, seeded.Add(1))
}

// SeedAdmin writes one admin who has opened the console before.
func SeedAdmin(t *testing.T, pg *gorm.DB, subject, name string) *model.Admin {
	t.Helper()

	now := data.Now()
	a := &model.Admin{ID: ids.New(ids.Admin), Subject: subject, Name: name, CreatedAt: now, SeenAt: &now}
	err := pg.Exec(`INSERT INTO admins (id, sso_subject, name, email, created_at, seen_at)
	                VALUES (?, ?, ?, '', ?, ?)`, a.ID, a.Subject, a.Name, now, now).Error
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// SeedGame writes one game as a manifest describes it, published, with the
// tagline and the icon publishing needs. The manifest's own address is
// derived from its entry, so a re-read has somewhere to go. The secret is
// "sec_" + the id, hashed, so a test can spend a code as the game's server.
func SeedGame(t *testing.T, pg *gorm.DB, id string, m model.Manifest) *model.Game {
	t.Helper()

	m.ID = id
	if m.Mode == "" {
		m.Mode = enum.ModeEmbedded
	}
	if m.Orientation == "" {
		m.Orientation = enum.Portrait
	}
	if m.Room.State == "" {
		m.Room.State = enum.StateBlob
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	publisher := m.Publisher.Name
	if publisher == "" {
		publisher = m.Publisher.Company
	}

	now := data.Now()
	g := &model.Game{
		ID: id, Name: m.Name, Tagline: "لعبة للتجربة", Publisher: publisher,
		IconKey: "games/" + id + "/icon.png", Category: enum.CategoryOther,
		Orientation: m.Orientation, Splash: m.Splash,
		Version: m.Version, Listing: enum.Published,
		FirstParty: m.FirstParty(), Tier: m.Publisher.Tier,
		ManifestURL: strings.TrimSuffix(m.Entry, "/") + model.WellKnown, Entry: m.Entry,
		Manifest: raw, FetchedAt: &now,
		Scopes: m.Scopes, Permissions: m.Permissions, Room: m.Room,
		SecretHash: model.HashSecret(SecretFor(id)), RedirectURIs: m.RedirectURIs,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := pg.Create(g).Error; err != nil {
		t.Fatal(err)
	}
	return g
}

// SecretFor is the client secret SeedGame gave a game.
func SecretFor(id string) string { return "sec_" + id }

// SeedLibrary puts a game on a player's shelf, opened once.
func SeedLibrary(t *testing.T, pg *gorm.DB, playerID, gameID string) *model.Library {
	t.Helper()

	now := data.Now()
	row := &model.Library{PlayerID: playerID, GameID: gameID, AddedAt: now, LastPlayedAt: &now, Plays: 1}
	if err := pg.Create(row).Error; err != nil {
		t.Fatal(err)
	}
	return row
}
