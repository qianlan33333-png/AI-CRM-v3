package domain

import "time"

type ScheduledConfiguration struct {
	PackageID              int64
	ConfigurationVersionID int64
	CronUTC                string
	Actor                  int64
	ConfigurationCreatedAt time.Time
	NextDueAt              *time.Time
	ScheduleVersion        int64
}
