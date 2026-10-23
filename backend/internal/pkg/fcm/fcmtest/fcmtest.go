// Package fcmtest is Firebase for tests: the OAuth token exchange a service
// account's key makes, and messages:send, every message kept and every
// answer scriptable per token. The e2e sweep serves one too. Only tests
// import it.
package fcmtest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jamaa/gathering/backend/internal/pkg/fcm"
)

// What the fake's own key says about itself.
const (
	Project = "gathering-test"
	KeyID   = "fake-key-1"
	Account = "pusher@gathering-test.iam.gserviceaccount.com"
)

// Reply is what the fake answers one send.
type Reply struct {
	Status int
	Body   string
}

// Firebase's answers, as its HTTP v1 API words them.
var (
	OK           = Reply{Status: http.StatusOK}
	Unregistered = googleError(404, "NOT_FOUND", "Requested entity was not found.", "UNREGISTERED", "")
	// NotFound is a 404 for the token without Firebase's own code.
	NotFound     = googleError(404, "NOT_FOUND", "Requested entity was not found.", "", "")
	InvalidToken = googleError(400, "INVALID_ARGUMENT",
		"The registration token is not a valid FCM registration token", "INVALID_ARGUMENT", "message.token")
	// InvalidMessage is a bad message whose token is fine.
	InvalidMessage = googleError(400, "INVALID_ARGUMENT",
		"Invalid value at 'message.data[0].value' (TYPE_STRING)", "INVALID_ARGUMENT", "message.data[0].value")
	SenderMismatch = googleError(403, "PERMISSION_DENIED", "SenderId mismatch", "SENDER_ID_MISMATCH", "")
	QuotaExceeded  = googleError(429, "RESOURCE_EXHAUSTED", "Quota exceeded.", "QUOTA_EXCEEDED", "")
	Unavailable    = googleError(503, "UNAVAILABLE", "The service is currently unavailable.", "UNAVAILABLE", "")
	Internal       = googleError(500, "INTERNAL", "Internal error encountered.", "INTERNAL", "")
)

func googleError(status int, code, message, fcmCode, field string) Reply {
	var details []map[string]any
	if fcmCode != "" {
		details = append(details, map[string]any{
			"@type": "type.googleapis.com/google.firebase.fcm.v1.FcmError", "errorCode": fcmCode,
		})
	}
	if field != "" {
		details = append(details, map[string]any{
			"@type":           "type.googleapis.com/google.rpc.BadRequest",
			"fieldViolations": []map[string]string{{"field": field, "description": message}},
		})
	}
	b, _ := json.Marshal(map[string]any{"error": map[string]any{
		"code": status, "message": message, "status": code, "details": details,
	}})
	return Reply{Status: status, Body: string(b)}
}

// Sent is one message the fake was sent, and what it answered.
type Sent struct {
	Project string
	Token   string
	// Message is the message as it arrived, read as JSON.
	Message map[string]any
	Status  int
}

// Exchange is one JWT the fake traded for an access token, read.
type Exchange struct {
	Header map[string]any
	Claims map[string]any
}

// Fake is the fake. Its URL is the endpoint to send to and, with /token, the
// token_uri of its key.
type Fake struct {
	URL string

	// Key is the service account's key the assertions are signed with. Nil
	// takes any signature: the sweep's fake, whose api holds a throwaway key
	// the sweep never sees.
	Key *rsa.PrivateKey

	mu          sync.Mutex
	issued      map[string]bool
	exchanges   []Exchange
	sent        []Sent
	answers     map[string][]Reply
	held        chan struct{}
	inFlight    int
	maxInFlight int
}

// sharedKey is made once per test binary: a 2048-bit key is slow to make,
// and every fake may as well hold the same one.
var sharedKey = sync.OnceValues(func() (*rsa.PrivateKey, error) { return rsa.GenerateKey(rand.Reader, 2048) })

// New starts a fake on a port of its own, holding a key it checks every
// assertion against, and stops it when the test ends.
func New(t *testing.T) *Fake {
	t.Helper()
	key, err := sharedKey()
	if err != nil {
		t.Fatal(err)
	}
	f := newFake(key)
	srv := httptest.NewServer(f.routes())
	f.URL = srv.URL
	t.Cleanup(srv.Close)
	t.Cleanup(f.Release)
	return f
}

// Listen starts a fake on a fixed address, the one the api under the sweep
// was pointed at (FCM_ENDPOINT, and its key's token_uri), taking any key.
func Listen(t *testing.T, addr string) *Fake {
	t.Helper()
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("fcmtest: listening on %s: %v", addr, err)
	}
	f := newFake(nil)
	srv := httptest.NewUnstartedServer(f.routes())
	_ = srv.Listener.Close()
	srv.Listener = l
	srv.Start()
	f.URL = srv.URL
	t.Cleanup(srv.Close)
	t.Cleanup(f.Release)
	return f
}

func newFake(key *rsa.PrivateKey) *Fake {
	return &Fake{Key: key, issued: map[string]bool{}, answers: map[string][]Reply{}}
}

func (f *Fake) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", f.token)
	mux.HandleFunc("POST /v1/projects/{project}/messages:send", f.send)
	return mux
}

// ServiceAccount is a service account's JSON key, as the Firebase console
// hands one out, for the fake's key and with the fake as its token_uri.
func (f *Fake) ServiceAccount() []byte {
	if f.Key == nil {
		panic("fcmtest: a fake without a key has no service account")
	}
	der, err := x509.MarshalPKCS8PrivateKey(f.Key)
	if err != nil {
		panic(err)
	}
	b, err := json.Marshal(map[string]string{
		"type": "service_account", "project_id": Project, "private_key_id": KeyID,
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email": Account, "client_id": "100000000000000000001",
		"auth_uri":  "https://accounts.google.com/o/oauth2/auth",
		"token_uri": f.URL + "/token",
	})
	if err != nil {
		panic(err)
	}
	return b
}

// Answer scripts the next answers to sends to a token, in order. After them
// the token is answered 200 again.
func (f *Fake) Answer(token string, replies ...Reply) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[token] = append(f.answers[token], replies...)
}

// Hold makes every send that arrives wait until Release.
func (f *Fake) Hold() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held == nil {
		f.held = make(chan struct{})
	}
}

// Release lets every held send through.
func (f *Fake) Release() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held != nil {
		close(f.held)
		f.held = nil
	}
}

// InFlight is how many sends the fake is answering right now; MaxInFlight
// the most it ever answered at once.
func (f *Fake) InFlight() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inFlight
}

func (f *Fake) MaxInFlight() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxInFlight
}

// Sent is every message answered so far, in the order answered.
func (f *Fake) Sent() []Sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Sent(nil), f.sent...)
}

// To is the messages answered so far to one token.
func (f *Fake) To(token string) []Sent {
	var out []Sent
	for _, s := range f.Sent() {
		if s.Token == token {
			out = append(out, s)
		}
	}
	return out
}

// Exchanges is every JWT traded for a token so far.
func (f *Fake) Exchanges() []Exchange {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Exchange(nil), f.exchanges...)
}

// Wait waits up to d for n messages in all to have been answered, and
// answers them.
func (f *Fake) Wait(t *testing.T, n int, d time.Duration) []Sent {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		got := f.Sent()
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("fcmtest: %d messages after %s, want %d", len(got), d, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// WaitFor waits up to d for a message to a token, and answers the first.
func (f *Fake) WaitFor(t *testing.T, token string, d time.Duration) Sent {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		if got := f.To(token); len(got) > 0 {
			return got[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("fcmtest: nothing sent to the token after %s", d)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// token is the OAuth 2 JWT bearer grant a service account's key makes.
func (f *Fake) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, "invalid_request", err.Error())
		return
	}
	if g := r.PostForm.Get("grant_type"); g != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		oauthError(w, "unsupported_grant_type", g)
		return
	}
	ex, err := f.read(r.PostForm.Get("assertion"))
	if err != nil {
		oauthError(w, "invalid_grant", err.Error())
		return
	}
	f.mu.Lock()
	f.exchanges = append(f.exchanges, ex)
	access := fmt.Sprintf("fake-access-%d", len(f.exchanges))
	f.issued[access] = true
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 3600})
}

// read checks an assertion the way Google's token endpoint does, as far as a
// test cares: RS256, signed by the key, for the messaging scope, addressed
// to this endpoint, not expired.
func (f *Fake) read(assertion string) (Exchange, error) {
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		return Exchange{}, fmt.Errorf("not a JWT")
	}
	var ex Exchange
	for i, into := range []*map[string]any{&ex.Header, &ex.Claims} {
		raw, err := base64.RawURLEncoding.DecodeString(parts[i])
		if err != nil {
			return Exchange{}, fmt.Errorf("part %d: %v", i, err)
		}
		if err := json.Unmarshal(raw, into); err != nil {
			return Exchange{}, fmt.Errorf("part %d: %v", i, err)
		}
	}
	if ex.Header["alg"] != "RS256" {
		return Exchange{}, fmt.Errorf("alg %v", ex.Header["alg"])
	}
	if f.Key != nil {
		sig, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil {
			return Exchange{}, fmt.Errorf("signature: %v", err)
		}
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if err := rsa.VerifyPKCS1v15(&f.Key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
			return Exchange{}, fmt.Errorf("signature: %v", err)
		}
		if ex.Claims["aud"] != f.URL+"/token" {
			return Exchange{}, fmt.Errorf("aud %v", ex.Claims["aud"])
		}
	}
	if ex.Claims["scope"] != fcm.Scope {
		return Exchange{}, fmt.Errorf("scope %v", ex.Claims["scope"])
	}
	if exp, _ := ex.Claims["exp"].(float64); int64(exp) < time.Now().Unix() {
		return Exchange{}, fmt.Errorf("expired")
	}
	return ex, nil
}

func oauthError(w http.ResponseWriter, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": description})
}

// send is messages:send: a token the fake issued, one message, and the
// answer scripted for its token or 200.
func (f *Fake) send(w http.ResponseWriter, r *http.Request) {
	access, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	known := f.issued[access]
	f.mu.Unlock()
	if !known {
		reply(w, googleError(401, "UNAUTHENTICATED", "Request had invalid authentication credentials.", "", ""))
		return
	}
	var body struct {
		Message map[string]any `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Message == nil {
		reply(w, googleError(400, "INVALID_ARGUMENT", "Invalid JSON payload received.", "", ""))
		return
	}
	token, _ := body.Message["token"].(string)

	f.mu.Lock()
	f.inFlight++
	f.maxInFlight = max(f.maxInFlight, f.inFlight)
	held := f.held
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inFlight--
		f.mu.Unlock()
	}()
	if held != nil {
		select {
		case <-held:
		case <-r.Context().Done():
			return
		}
	}

	project := r.PathValue("project")
	f.mu.Lock()
	answer := Reply{Status: http.StatusOK, Body: fmt.Sprintf(`{"name":"projects/%s/messages/%d"}`, project, len(f.sent)+1)}
	if q := f.answers[token]; len(q) > 0 {
		answer, f.answers[token] = q[0], q[1:]
		if answer.Body == "" {
			answer.Body = "{}"
		}
	}
	f.sent = append(f.sent, Sent{Project: project, Token: token, Message: body.Message, Status: answer.Status})
	f.mu.Unlock()
	reply(w, answer)
}

func reply(w http.ResponseWriter, r Reply) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(r.Status)
	_, _ = w.Write([]byte(r.Body))
}
