package enum

// GameCategory is how the store groups a game. A closed list, so the store's
// sections are ours and a studio cannot invent a ninth. Adding one is a line
// below; the console and the docs read this list.
type GameCategory string

const (
	CategorySpeed GameCategory = "speed"
	CategoryBrain GameCategory = "brain"
	CategoryWords GameCategory = "words"
	CategoryLuck  GameCategory = "luck"
	CategoryGuess GameCategory = "guess"
	CategoryDraw  GameCategory = "draw"
	CategoryParty GameCategory = "party"
	CategoryOther GameCategory = "other"
)

// Categories is the list in the order the store shows it. Other is last: it
// is where a newly registered game waits until an admin files it.
var Categories = []GameCategory{
	CategorySpeed,
	CategoryBrain,
	CategoryWords,
	CategoryLuck,
	CategoryGuess,
	CategoryDraw,
	CategoryParty,
	CategoryOther,
}

func (c GameCategory) Valid() bool { return in(Categories, c) }

// Listing is what the registry thinks of a game. Only Published is visible to
// a player — the other two are a 404, not a 403.
type Listing string

const (
	Published   Listing = "published"
	Unpublished Listing = "unpublished"
	Suspended   Listing = "suspended"
)

var Listings = []Listing{Published, Unpublished, Suspended}

func (l Listing) Valid() bool { return in(Listings, l) }

// Tier is who made the game. Only a first-party game may be featured on the
// strength of its tier alone, so the tier is what the registry checks rather
// than anybody's word for it.
type Tier string

const (
	TierFirstParty Tier = "first-party"
	TierPartner    Tier = "partner"
)

var Tiers = []Tier{TierFirstParty, TierPartner}

func (t Tier) Valid() bool { return in(Tiers, t) }

// Mode is how a game opens. There is one: inside the app, in a frame. The
// field stays in the manifest so a second mode, if one comes, is a value and
// not a schema change.
type Mode string

const ModeEmbedded Mode = "embedded"

var Modes = []Mode{ModeEmbedded}

func (m Mode) Valid() bool { return in(Modes, m) }

// Orientation is how the frame is turned when the game opens.
type Orientation string

const (
	Portrait  Orientation = "portrait"
	Landscape Orientation = "landscape"
)

var Orientations = []Orientation{Portrait, Landscape}

func (o Orientation) Valid() bool { return in(Orientations, o) }

// HostMode says who may write a room's shared state: the host alone, or any
// member.
type HostMode string

const (
	HostAuthoritative HostMode = "authoritative"
	HostPeer          HostMode = "peer"
)

var HostModes = []HostMode{HostAuthoritative, HostPeer}

func (h HostMode) Valid() bool { return in(HostModes, h) }

// Pace is how a game moves: in turns, or all at once. The relay is the same
// either way; players read it before they join.
type Pace string

const (
	PaceTurn     Pace = "turn"
	PaceRealtime Pace = "realtime"
)

var Paces = []Pace{PaceTurn, PaceRealtime}

func (p Pace) Valid() bool { return in(Paces, p) }

// StateMode says whether a room keeps a versioned shared state for the game,
// or refuses state frames outright.
type StateMode string

const (
	StateBlob StateMode = "blob"
	StateNone StateMode = "none"
)

var StateModes = []StateMode{StateBlob, StateNone}

func (s StateMode) Valid() bool { return in(StateModes, s) }

// Permission is a device permission a game may ask the player for. The app
// asks on the game's behalf; a manifest naming one outside this list is
// refused at registration.
type Permission string

const (
	PermissionCamera       Permission = "camera"
	PermissionMicrophone   Permission = "microphone"
	PermissionLocation     Permission = "location"
	PermissionNotification Permission = "notification"
	PermissionStorage      Permission = "storage"
	PermissionContacts     Permission = "contacts"
)

var Permissions = []Permission{
	PermissionCamera,
	PermissionMicrophone,
	PermissionLocation,
	PermissionNotification,
	PermissionStorage,
	PermissionContacts,
}

func (p Permission) Valid() bool { return in(Permissions, p) }
