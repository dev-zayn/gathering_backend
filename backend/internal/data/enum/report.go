package enum

// ReportReason is why a player reported another (docs/REPORTS.md).
type ReportReason string

const (
	ReportName       ReportReason = "name"
	ReportPhoto      ReportReason = "photo"
	ReportHarassment ReportReason = "harassment"
	ReportCheating   ReportReason = "cheating"
	ReportOther      ReportReason = "other"
)

var ReportReasons = []ReportReason{ReportName, ReportPhoto, ReportHarassment, ReportCheating, ReportOther}

func (r ReportReason) Valid() bool { return in(ReportReasons, r) }

// ReportStatus is whether an admin has dealt with a report yet.
type ReportStatus string

const (
	ReportOpen     ReportStatus = "open"
	ReportResolved ReportStatus = "resolved"
)

var ReportStatuses = []ReportStatus{ReportOpen, ReportResolved}

func (s ReportStatus) Valid() bool { return in(ReportStatuses, s) }
