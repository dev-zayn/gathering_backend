package fcm_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"github.com/jamaa/gathering/backend/internal/pkg/fcm"
	"github.com/jamaa/gathering/backend/internal/pkg/fcm/fcmtest"
)

func client(t *testing.T, fake *fcmtest.Fake) *fcm.Client {
	t.Helper()
	c, err := fcm.New(fcm.Config{ProjectID: "gathering-supergame", Credentials: fake.ServiceAccount(), Endpoint: fake.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func message(token string) fcm.Message {
	badge := 3
	return fcm.Message{
		Token:        token,
		Notification: &fcm.Notification{Title: "طلب صداقة", Body: "زين يريد يصير صديقك"},
		Data:         map[string]string{"notification": "ntf_7f3k9d2m8q1x4b6n", "kind": "friend_request"},
		Android: &fcm.AndroidConfig{Priority: "HIGH",
			Notification: &fcm.AndroidNotification{ChannelID: "inbox", Tag: "friend_request:ply_1"}},
		APNS: &fcm.APNSConfig{Headers: map[string]string{"apns-collapse-id": "friend_request:ply_1"},
			Payload: &fcm.APNSPayload{Aps: fcm.Aps{Sound: "default", Badge: &badge}}},
	}
}

// A send signs a JWT with the service account's key, trades it at the key's
// token_uri for an access token (the messaging scope, the key's id in the
// header, the account as its issuer), and posts the message under the
// project with that token. The token is kept: three sends, one exchange.
func TestASendTradesTheKeyOnceAndPostsTheMessage(t *testing.T) {
	fake := fcmtest.New(t)
	c := client(t, fake)
	ctx := context.Background()

	for _, token := range []string{"tok-a", "tok-b", "tok-c"} {
		if err := c.Send(ctx, message(token)); err != nil {
			t.Fatalf("send to %s: %v", token, err)
		}
	}

	ex := fake.Exchanges()
	if len(ex) != 1 {
		t.Fatalf("%d token exchanges for three sends, want 1", len(ex))
	}
	if ex[0].Header["kid"] != fcmtest.KeyID || ex[0].Claims["iss"] != fcmtest.Account ||
		ex[0].Claims["scope"] != fcm.Scope || ex[0].Claims["aud"] != fake.URL+"/token" {
		t.Fatalf("the assertion: %+v", ex[0])
	}

	sent := fake.Sent()
	if len(sent) != 3 || sent[0].Project != "gathering-supergame" || sent[0].Token != "tok-a" || sent[0].Status != 200 {
		t.Fatalf("sent: %+v", sent)
	}
	want := map[string]any{}
	raw, _ := json.Marshal(message("tok-a"))
	_ = json.Unmarshal(raw, &want)
	if got, _ := json.Marshal(sent[0].Message); string(got) != string(mustJSON(t, want)) {
		t.Fatalf("the message arrived as\n%s\nwant\n%s", got, mustJSON(t, want))
	}
	aps := sent[0].Message["apns"].(map[string]any)["payload"].(map[string]any)["aps"].(map[string]any)
	if aps["badge"] != float64(3) || aps["sound"] != "default" {
		t.Fatalf("aps: %+v", aps)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A badge of zero is sent as zero: it clears the number on the icon.
func TestABadgeOfZeroIsSent(t *testing.T) {
	fake := fcmtest.New(t)
	m := message("tok-zero")
	zero := 0
	m.APNS.Payload.Aps.Badge = &zero
	if err := client(t, fake).Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	aps := fake.Sent()[0].Message["apns"].(map[string]any)["payload"].(map[string]any)["aps"].(map[string]any)
	if b, ok := aps["badge"]; !ok || b != float64(0) {
		t.Fatalf("aps: %+v", aps)
	}
}

// Each refusal says whether the token is gone for good and whether trying
// again may work; a 404 that is not the API's own error (a wrong endpoint)
// is neither, and the token never appears in what is said.
func TestRefusalsAreSorted(t *testing.T) {
	fake := fcmtest.New(t)
	c := client(t, fake)
	ctx := context.Background()

	for _, tc := range []struct {
		name            string
		reply           fcmtest.Reply
		code            string
		gone, temporary bool
	}{
		{"unregistered", fcmtest.Unregistered, "UNREGISTERED", true, false},
		{"a 404 for the token", fcmtest.NotFound, "NOT_FOUND", true, false},
		{"a token that never was", fcmtest.InvalidToken, "INVALID_ARGUMENT", true, false},
		{"a bad message", fcmtest.InvalidMessage, "INVALID_ARGUMENT", false, false},
		{"another project's token", fcmtest.SenderMismatch, "SENDER_ID_MISMATCH", false, false},
		{"over the quota", fcmtest.QuotaExceeded, "QUOTA_EXCEEDED", false, true},
		{"unavailable", fcmtest.Unavailable, "UNAVAILABLE", false, true},
		{"internal", fcmtest.Internal, "INTERNAL", false, true},
		{"not the API's 404", fcmtest.Reply{Status: 404, Body: "<html>Not Found</html>"}, "", false, false},
		{"not the API's 502", fcmtest.Reply{Status: 502, Body: "Bad Gateway"}, "", false, true},
	} {
		token := "tok-" + strings.ReplaceAll(tc.name, " ", "-")
		fake.Answer(token, tc.reply)
		err := c.Send(ctx, message(token))
		var e *fcm.Error
		if !errors.As(err, &e) {
			t.Fatalf("%s: got %v, want an *fcm.Error", tc.name, err)
		}
		if e.Status != tc.reply.Status || e.Code != tc.code || e.Gone() != tc.gone || e.Temporary() != tc.temporary {
			t.Fatalf("%s: %+v gone=%v temporary=%v", tc.name, e, e.Gone(), e.Temporary())
		}
		if strings.Contains(err.Error(), token) {
			t.Fatalf("%s: the token is in %q", tc.name, err)
		}
	}

	// Firebase quoting the token: cut out.
	quoted := fcmtest.Reply{Status: 400, Body: `{"error":{"code":400,"status":"INVALID_ARGUMENT",` +
		`"message":"The registration token tok-quoted-123 is not a valid FCM registration token"}}`}
	fake.Answer("tok-quoted-123", quoted)
	err := c.Send(ctx, message("tok-quoted-123"))
	var e *fcm.Error
	if !errors.As(err, &e) || !e.Gone() || strings.Contains(err.Error(), "tok-quoted-123") {
		t.Fatalf("a refusal quoting the token: %v", err)
	}
}

// A token endpoint that refuses the key (here, one signed by a key the
// account does not have) is a plain error, not Firebase refusing the
// message, and it quotes no key; nothing is sent without a token.
func TestARefusedKeyIsAPlainError(t *testing.T) {
	fake := fcmtest.New(t)
	stranger, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(stranger)
	if err != nil {
		t.Fatal(err)
	}
	var key map[string]string
	_ = json.Unmarshal(fake.ServiceAccount(), &key)
	key["private_key"] = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	raw, _ := json.Marshal(key)
	c, err := fcm.New(fcm.Config{ProjectID: "p", Credentials: raw, Endpoint: fake.URL})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Send(context.Background(), message("tok-a"))
	var e *fcm.Error
	if err == nil || errors.As(err, &e) || !strings.Contains(err.Error(), "invalid_grant") ||
		strings.Contains(err.Error(), "PRIVATE KEY") {
		t.Fatalf("got %v", err)
	}
	if len(fake.Sent()) != 0 || len(fake.Exchanges()) != 0 {
		t.Fatal("a message went without a token")
	}
}

// A bad key or endpoint stops the api at boot, with a reason that quotes no
// key.
func TestABadConfigIsRefusedAtOnce(t *testing.T) {
	fake := fcmtest.New(t)
	var good map[string]string
	_ = json.Unmarshal(fake.ServiceAccount(), &good)
	with := func(field, value string) []byte {
		k := map[string]string{}
		for f, v := range good {
			k[f] = v
		}
		if value == "" {
			delete(k, field)
		} else {
			k[field] = value
		}
		b, _ := json.Marshal(k)
		return b
	}

	for _, tc := range []struct {
		name string
		cfg  fcm.Config
		want string
	}{
		{"no project", fcm.Config{Credentials: fake.ServiceAccount()}, "no project id"},
		{"not JSON", fcm.Config{ProjectID: "p", Credentials: []byte("-----BEGIN PRIVATE KEY-----")}, "not a JSON key"},
		{"not a service account", fcm.Config{ProjectID: "p", Credentials: with("type", "authorized_user")}, "not a service account"},
		{"no account", fcm.Config{ProjectID: "p", Credentials: with("client_email", "")}, "client_email"},
		{"no key", fcm.Config{ProjectID: "p", Credentials: with("private_key", "")}, "private_key"},
		{"not a key", fcm.Config{ProjectID: "p", Credentials: with("private_key", "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n")}, "private_key"},
		{"a token_uri that is no address", fcm.Config{ProjectID: "p", Credentials: with("token_uri", "oauth2.googleapis.com/token")}, "token_uri"},
		{"an endpoint that is no address", fcm.Config{ProjectID: "p", Credentials: fake.ServiceAccount(), Endpoint: "fcm.googleapis.com"}, "endpoint"},
	} {
		_, err := fcm.New(tc.cfg)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: got %v, want %q", tc.name, err, tc.want)
		}
		if strings.Contains(err.Error(), "PRIVATE KEY") || strings.Contains(err.Error(), good["private_key"][40:80]) {
			t.Fatalf("%s: the key is in %q", tc.name, err)
		}
	}

	// No token_uri is Google's, and no endpoint is Firebase's: both taken.
	if _, err := fcm.New(fcm.Config{ProjectID: "p", Credentials: with("token_uri", "")}); err != nil {
		t.Fatalf("a key without a token_uri, and no endpoint: %v", err)
	}
}
