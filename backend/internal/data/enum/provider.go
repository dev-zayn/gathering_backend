package enum

// Provider is who vouched for a player who signed in without a phone
// (docs/SSO.md). The pair (provider, the provider's subject) is the player.
type Provider string

const (
	ProviderGoogle Provider = "google"
	ProviderApple  Provider = "apple"
)

var Providers = []Provider{ProviderGoogle, ProviderApple}

func (p Provider) Valid() bool { return in(Providers, p) }
