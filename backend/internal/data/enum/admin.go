package enum

// AdminRole is what a person holds in the console. The identity provider says
// which they have; what each may do is decided here. Both may do everything
// today.
type AdminRole string

const (
	AdminRoleAdmin    AdminRole = "admin"
	AdminRoleOperator AdminRole = "operator"
)

var AdminRoles = []AdminRole{AdminRoleAdmin, AdminRoleOperator}

func (r AdminRole) Valid() bool { return in(AdminRoles, r) }
