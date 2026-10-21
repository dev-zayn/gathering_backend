package outbound

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The addresses a studio must not be able to send us to. 169.254.169.254 is
// the one that matters most: it is where a cloud keeps the credentials of the
// machine doing the fetching.
func TestAStudioCannotNameAnAddressOnlyWeCanReach(t *testing.T) {
	c := Client(2*time.Second, false)

	for _, address := range []string{
		"http://169.254.169.254/latest/meta-data/iam/security-credentials/",
		"http://127.0.0.1:5432/",
		"http://10.0.0.1/",
		"http://192.168.1.1/",
		"http://[::1]/",
		"http://[::ffff:169.254.169.254]/",
		"http://100.64.0.1/",
		"http://0.0.0.0/",
	} {
		_, err := c.Get(address)
		if err == nil {
			t.Errorf("%s was reached", address)
			continue
		}
		if !strings.Contains(err.Error(), "not a public address") {
			t.Errorf("%s was refused for the wrong reason: %v", address, err)
		}
	}
}

// The guard is on the dial, not on the URL, so a public name that redirects
// inwards is caught on the second hop rather than waved through on the first.
func TestARedirectCannotWalkUsInwards(t *testing.T) {
	inside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("the metadata service"))
	}))
	defer inside.Close()

	c := Client(2*time.Second, false)
	if _, err := c.Get(inside.URL); err == nil {
		t.Fatal("a loopback server was reached directly")
	}

	// And through a redirect, which is the form that looks public at first.
	hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, inside.URL, http.StatusFound)
	}))
	defer hop.Close()
	if _, err := c.Get(hop.URL); err == nil {
		t.Fatal("a redirect reached a loopback address")
	}
}

// A laptop has to be able to reach the fixtures, which are another container on
// a private address. That is the whole of what the switch is for.
func TestALaptopCanStillReachItsFixtures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	res, err := Client(2*time.Second, true).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
}

// A chain that never arrives is a studio walking us somewhere, or a loop.
func TestTheRedirectChainIsBounded(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL, http.StatusFound)
	}))
	defer srv.Close()

	_, err := Client(2*time.Second, true).Get(srv.URL)
	if err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Fatalf("an endless redirect chain was followed: %v", err)
	}
}

func TestWhatIsNotAPublicAddress(t *testing.T) {
	blocked := []string{
		"127.0.0.1",              // ourselves
		"::1",                    //
		"169.254.169.254",        // the cloud metadata endpoint, the whole reason
		"::ffff:169.254.169.254", // the same, written the IPv6 way
		"fe80::1",                // link-local
		"10.0.0.5",               // a private subnet
		"172.16.0.5",             //
		"192.168.1.5",            //
		"fd00::1",                // unique local
		"0.0.0.0",                // unspecified
		"::",                     //
		"100.100.100.200",        // carrier-grade NAT
		"224.0.0.1",              // multicast
		"255.255.255.255",        // broadcast
		"64:ff9b::a9fe:a9fe",     // metadata again, through the translation prefix
	}
	for _, raw := range blocked {
		ip := net.ParseIP(raw)
		if ip == nil {
			t.Fatalf("%s is not an address at all — the test is wrong", raw)
		}
		if public(ip) {
			t.Errorf("%s counts as somebody else's server", raw)
		}
	}
}

func TestWhatIsAPublicAddress(t *testing.T) {
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "185.60.216.35", "2606:4700::1111"} {
		ip := net.ParseIP(raw)
		if ip == nil {
			t.Fatalf("%s is not an address at all — the test is wrong", raw)
		}
		if !public(ip) {
			t.Errorf("%s is refused; a real publisher could be there", raw)
		}
	}
}
