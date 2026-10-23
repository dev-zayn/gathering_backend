package fcm

// Message is one FCM HTTP v1 message to one phone: the parts of the API's
// message that a push here uses, named as the API names them.
type Message struct {
	// FID is the phone's Firebase installation id, the target FCM asks for
	// now; Token is the older registration token, which the API still
	// takes. One of the two is set (To sets the right one).
	FID          string            `json:"fid,omitempty"`
	Token        string            `json:"token,omitempty"`
	Notification *Notification     `json:"notification,omitempty"`
	Data         map[string]string `json:"data,omitempty"`
	Android      *AndroidConfig    `json:"android,omitempty"`
	APNS         *APNSConfig       `json:"apns,omitempty"`
}

// Notification is what the phone shows when the app is not on screen.
type Notification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// AndroidConfig is what only Android reads.
type AndroidConfig struct {
	// Priority is HIGH or NORMAL.
	Priority     string               `json:"priority,omitempty"`
	Notification *AndroidNotification `json:"notification,omitempty"`
}

// AndroidNotification is the notification's channel, and its tag: a second
// notification with the same tag replaces the first in the tray.
type AndroidNotification struct {
	ChannelID string `json:"channel_id,omitempty"`
	Tag       string `json:"tag,omitempty"`
}

// APNSConfig is what only Apple's service reads: its headers
// (apns-collapse-id) and the payload's aps dictionary.
type APNSConfig struct {
	Headers map[string]string `json:"headers,omitempty"`
	Payload *APNSPayload      `json:"payload,omitempty"`
}

type APNSPayload struct {
	Aps Aps `json:"aps"`
}

// Aps is the sound, and the number on the app's icon. A nil Badge leaves
// the number as it is; zero clears it.
type Aps struct {
	Sound string `json:"sound,omitempty"`
	Badge *int   `json:"badge,omitempty"`
}

// To addresses the message to [address]: as `fid` when it is a Firebase
// installation id, else as the older `token`. The app sends the FID that
// firebase_messaging's onRegistered gives; a registration token from an
// older app still goes as a token.
func (m *Message) To(address string) {
	m.FID, m.Token = "", ""
	if IsFID(address) {
		m.FID = address
		return
	}
	m.Token = address
}

// Target is whichever address the message is sent to.
func (m Message) Target() string {
	if m.FID != "" {
		return m.FID
	}
	return m.Token
}

// IsFID says whether [s] reads as a Firebase installation id: 22 symbols of
// URL-safe base64, the first of them c, d, e or f (its top bits are 0111).
// A registration token is far longer and carries a colon.
func IsFID(s string) bool {
	if len(s) != 22 || s[0] < 'c' || s[0] > 'f' {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}
