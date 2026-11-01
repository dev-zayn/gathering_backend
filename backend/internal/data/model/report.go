package model

import (
	"time"

	"github.com/jamaa/gathering/backend/internal/data/enum"
)

// MaxReportNote is the longest note a report carries.
const MaxReportNote = 300

// Report is a player reporting another (docs/REPORTS.md).
type Report struct {
	ID         string `gorm:"primaryKey"`
	ReporterID string
	PlayerID   string
	Reason     enum.ReportReason
	Note       string
	Status     enum.ReportStatus
	CreatedAt  time.Time
	ResolvedAt *time.Time
	ResolvedBy *string
}

func (Report) TableName() string { return "reports" }
