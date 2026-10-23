package enum

// FriendshipStatus is where a pair stands: one of them asked and the other
// has not answered, or they are friends. Every other answer deletes the row.
type FriendshipStatus string

const (
	FriendshipRequested FriendshipStatus = "requested"
	FriendshipAccepted  FriendshipStatus = "accepted"
)

var FriendshipStatuses = []FriendshipStatus{FriendshipRequested, FriendshipAccepted}

func (s FriendshipStatus) Valid() bool { return in(FriendshipStatuses, s) }

// NoticeKind is what a notification is about: a friend asking, a friend
// accepting, an invite to a room, a game's own server, the platform, or a
// tournament (docs/TOURNAMENTS.md: an invite, its start, a match ready, its
// end).
type NoticeKind string

const (
	NoticeFriendRequest  NoticeKind = "friend_request"
	NoticeFriendAccepted NoticeKind = "friend_accepted"
	NoticeRoomInvite     NoticeKind = "room_invite"
	NoticeGame           NoticeKind = "game"
	NoticeSystem         NoticeKind = "system"
	NoticeTournament     NoticeKind = "tournament"
)

var NoticeKinds = []NoticeKind{
	NoticeFriendRequest, NoticeFriendAccepted, NoticeRoomInvite, NoticeGame, NoticeSystem, NoticeTournament,
}

func (k NoticeKind) Valid() bool { return in(NoticeKinds, k) }
