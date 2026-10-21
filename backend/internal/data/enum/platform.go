package enum

// Platform is the kind of phone a device is. Firebase reaches both; the push
// is shaped a little differently for each.
type Platform string

const (
	PlatformIOS     Platform = "ios"
	PlatformAndroid Platform = "android"
)

var Platforms = []Platform{PlatformIOS, PlatformAndroid}

func (p Platform) Valid() bool { return in(Platforms, p) }
