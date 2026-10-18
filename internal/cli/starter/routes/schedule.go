package routes

import (
	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/schedule"
)

// Schedule declares the scheduled tasks. "vukem serve" runs them in process;
// with SCHEDULE_IN_SERVE=false run "vukem schedule:work" or a cron entry
// calling "vukem schedule:run" every minute instead. List them with
// "vukem schedule:list". Tasks must honor ctx: on shutdown it is cancelled
// after SHUTDOWN_TIMEOUT. Name the parameters (s, app) with the first task
// you add. Examples:
//
//	s.Command("reports:send").DailyAt("08:00").WithoutOverlapping()
//	s.Job(jobs.PruneSessions{}).Hourly()
func Schedule(_ *schedule.Schedule, _ *vukem.App) {}
