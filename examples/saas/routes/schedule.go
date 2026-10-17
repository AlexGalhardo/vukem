package routes

import (
	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/schedule"
)

// Schedule declares the scheduled tasks. Run them with "vukem schedule:work"
// or a cron entry calling "vukem schedule:run" every minute; list them with
// "vukem schedule:list". Examples:
//
//	s.Command("reports:send").DailyAt("08:00").WithoutOverlapping()
//	s.Job(jobs.PruneSessions{}).Hourly()
func Schedule(s *schedule.Schedule, app *vukem.App) {}
