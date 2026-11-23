package enum

// BuildStatus is where a build of a game is on its way to players
// (docs/PUBLISHERS.md). The first four are open: a game has at most one.
type BuildStatus string

const (
	BuildTesting   BuildStatus = "testing"
	BuildInReview  BuildStatus = "in_review"
	BuildApproved  BuildStatus = "approved"
	BuildRejected  BuildStatus = "rejected"
	BuildPublished BuildStatus = "published"
	BuildWithdrawn BuildStatus = "withdrawn"
)

var BuildStatuses = []BuildStatus{BuildTesting, BuildInReview, BuildApproved, BuildRejected, BuildPublished, BuildWithdrawn}

// OpenBuilds are the statuses a build is still on its way in. The partial
// unique index game_builds_open names the same four.
var OpenBuilds = []BuildStatus{BuildTesting, BuildInReview, BuildApproved, BuildRejected}

func (s BuildStatus) Valid() bool { return in(BuildStatuses, s) }

// Open says the build is still on its way: testers play it.
func (s BuildStatus) Open() bool { return in(OpenBuilds, s) }

// Editable says the publisher may change it: anything open but in review.
func (s BuildStatus) Editable() bool { return s.Open() && s != BuildInReview }

// PublisherStatus is how the console reads an account: no password yet,
// signing in, or switched off. Not a column: it is read off the row.
type PublisherStatus string

const (
	PublisherInvited  PublisherStatus = "invited"
	PublisherActive   PublisherStatus = "active"
	PublisherDisabled PublisherStatus = "disabled"
)
