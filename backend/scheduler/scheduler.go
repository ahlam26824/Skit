// Package scheduler runs cron jobs that trigger medication reminders.
package scheduler

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"med-reminder/backend/handlers"

	"github.com/robfig/cron/v3"
)

// Scheduler manages cron jobs for active medication schedules.
type Scheduler struct {
	db   *sql.DB
	hub  *handlers.Hub
	cron *cron.Cron
	mu   sync.Mutex
}

// New creates a new Scheduler.
func New(db *sql.DB, hub *handlers.Hub) *Scheduler {
	return &Scheduler{db: db, hub: hub}
}

// Start loads schedules from DB and starts the cron engine.
func (s *Scheduler) Start() error {
	return s.Reload()
}

// Stop shuts down the cron engine.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cron != nil {
		s.cron.Stop()
	}
}

// Reload stops the current cron (if any), reloads all active schedules,
// and starts a fresh cron engine.
func (s *Scheduler) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cron != nil {
		s.cron.Stop()
	}

	// Use second-optional cron parser for standard minute-level cron expressions.
	s.cron = cron.New()

	rows, err := s.db.Query(`
		SELECT s.id, s.medication_id, m.name, s.time_of_day, s.days_of_week,
		       s.start_date, COALESCE(s.end_date, '') as end_date
		FROM schedules s
		JOIN medications m ON m.id = s.medication_id
		WHERE s.active = 1`)
	if err != nil {
		return fmt.Errorf("query schedules: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var schedID, medID int64
		var medName, tod, dow, startDate, endDate string
		if err := rows.Scan(&schedID, &medID, &medName, &tod, &dow, &startDate, &endDate); err != nil {
			log.Printf("scheduler scan: %v", err)
			continue
		}

		expr := toCronExpr(tod, dow)
		// Capture loop variables for closure.
		sid, mid, mn, sd, ed := schedID, medID, medName, startDate, endDate

		_, err := s.cron.AddFunc(expr, func() {
			// Check date range before firing.
			today := time.Now().Format("2006-01-02")
			if today < sd {
				return // hasn't started yet
			}
			if ed != "" && today > ed {
				return // already ended
			}
			log.Printf("Cron firing: %s (%s)", mn, tod)
			if _, err := s.hub.TriggerDevice(mid, sid, mn); err != nil {
				log.Printf("cron trigger error: %v", err)
			}
		})
		if err != nil {
			log.Printf("cron add %q: %v", expr, err)
			continue
		}
		count++
	}

	s.cron.Start()
	log.Printf("Scheduler loaded %d cron jobs", count)
	return nil
}

// toCronExpr converts time_of_day ("HH:MM") and days_of_week ("1,2,3,...,7")
// into a standard cron expression.
//
// Cron format: minute hour * * day-of-week
// Our days_of_week uses 1=Monday..7=Sunday (ISO).
// Standard cron uses 0=Sunday..6=Saturday.
func toCronExpr(tod, dow string) string {
	parts := strings.SplitN(tod, ":", 2)
	if len(parts) != 2 {
		return "0 0 * * *" // fallback: midnight daily
	}
	minute := strings.TrimLeft(parts[1], "0")
	if minute == "" {
		minute = "0"
	}
	hour := strings.TrimLeft(parts[0], "0")
	if hour == "" {
		hour = "0"
	}

	// Convert ISO day numbers to cron day numbers.
	cronDays := convertDays(dow)

	return fmt.Sprintf("%s %s * * %s", minute, hour, cronDays)
}

// convertDays converts "1,2,3,4,5,6,7" (ISO Mon=1..Sun=7) to cron "1,2,3,4,5,6,0".
func convertDays(dow string) string {
	if dow == "1,2,3,4,5,6,7" {
		return "*" // every day
	}
	parts := strings.Split(dow, ",")
	out := make([]string, 0, len(parts))
	for _, d := range parts {
		d = strings.TrimSpace(d)
		if d == "7" {
			out = append(out, "0") // Sunday
		} else {
			out = append(out, d)
		}
	}
	return strings.Join(out, ",")
}
