//go:build e2e

package e2e

import (
	"os"
	"testing"
	"time"

	"github.com/jamaa/gathering/backend/internal/pkg/fcm/fcmtest"
)

// TestPush is docs/PUSH.md against the running api: a phone registered on
// B's session, A's request in B's inbox. The api may run without a Firebase
// key, and then it only logs "would push" with the notification's id and
// the phone count, which the sweep cannot read; so the send itself is
// asserted only with E2E_FCM, the address the sweep serves a fake Firebase
// on and the api was started against (FCM_ENDPOINT=http://E2E_FCM, and an
// FCM_CREDENTIALS_FILE whose token_uri is http://E2E_FCM/token, written by
// scripts/fcm-test-key.sh). Then B's phone hears the request in the message
// the app is built to, and nothing more once B has signed out.
func TestPush(t *testing.T) {
	var fake *fcmtest.Fake
	if addr := os.Getenv("E2E_FCM"); addr != "" {
		fake = fcmtest.Listen(t, addr)
	}

	a, pa := player(t, "Nadia")
	b, pb := player(t, "Yousif")
	aID, bID := pa["id"].(string), pb["id"].(string)

	token := "e2e-push-" + bID
	var device struct {
		ID       string `json:"id"`
		Platform string `json:"platform"`
	}
	b.call("PUT", "/v1/me/device", map[string]any{"platform": "android", "token": token}, 200, &device)
	if len(device.ID) < 5 || device.ID[:4] != "dev_" || device.Platform != "android" {
		t.Fatalf("the phone: %+v", device)
	}
	t.Logf("ok   B's phone registered: %s", device.ID)

	a.call("POST", "/v1/friends/requests", map[string]any{"player": bID}, 201, nil)
	feed := inboxOf(t, b, "")
	if feed.Unread != 1 || len(feed.Items) != 1 {
		t.Fatalf("B's inbox: %+v", feed)
	}
	n := feed.Items[0]
	if n.Kind != "friend_request" || n.Title != "طلب صداقة" || n.Body != "Nadia يريد يصير صديقك" ||
		n.Data["player"] != aID || n.Read {
		t.Fatalf("the request: %+v", n)
	}
	t.Logf("ok   A asked B: %s in B's inbox", n.ID)

	if fake == nil {
		t.Logf("ok   E2E_FCM unset: the send is not asserted; the api's log has \"would push\" notification=%s devices=1 "+
			"if it runs without a key", n.ID)
		return
	}

	got := fake.WaitFor(t, token, 10*time.Second)
	data, _ := got.Message["data"].(map[string]any)
	alert, _ := got.Message["notification"].(map[string]any)
	android, _ := got.Message["android"].(map[string]any)
	apns, _ := got.Message["apns"].(map[string]any)
	if data["notification"] != n.ID || data["kind"] != "friend_request" || data["player"] != aID || len(data) != 3 ||
		alert["title"] != n.Title || alert["body"] != n.Body || android["priority"] != "HIGH" || apns == nil {
		t.Fatalf("the push: %+v", got.Message)
	}
	collapse := "friend_request:" + aID
	if tag := android["notification"].(map[string]any)["tag"]; tag != collapse {
		t.Fatalf("the tag: %v", tag)
	}
	if id := apns["headers"].(map[string]any)["apns-collapse-id"]; id != collapse {
		t.Fatalf("the collapse id: %v", id)
	}
	if badge := apns["payload"].(map[string]any)["aps"].(map[string]any)["badge"]; badge != float64(1) {
		t.Fatalf("the badge: %v", badge)
	}
	t.Logf("ok   the fake Firebase got the push for %s: the row's words, data, collapse %s, badge 1", n.ID, collapse)

	// Signed out: the session ended, and its phone hears nothing more.
	b.call("POST", "/v1/auth/logout", nil, 204, nil)
	c, _ := player(t, "Ibrahim")
	c.call("POST", "/v1/friends/requests", map[string]any{"player": bID}, 201, nil)
	time.Sleep(time.Second)
	if sent := fake.To(token); len(sent) != 1 {
		t.Fatalf("%d pushes to a signed-out phone's token, want the first only", len(sent))
	}
	t.Logf("ok   after B signed out, C's request pushed nothing to B's phone")
}
