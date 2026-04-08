package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Event represents an event row joined with medication name.
type Event struct {
	ID                int64  `json:"id"`
	MedicationID      int64  `json:"medicationId"`
	MedicationName    string `json:"medicationName"`
	ScheduleID        *int64 `json:"scheduleId"`
	ScheduledAt       string `json:"scheduledAt"`
	CompletedAt       string `json:"completedAt,omitempty"`
	Status            string `json:"status"`
	ConfirmedByDevice bool   `json:"confirmedByDevice"`
}

// EventHandler provides endpoints for the events table.
type EventHandler struct {
	DB  *sql.DB
	Hub *Hub // needed for status + debug trigger
}

// ListEvents — GET /api/events?days=7
func (h *EventHandler) ListEvents(c *gin.Context) {
	days := 7
	if d := c.Query("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 {
			days = parsed
		}
	}

	rows, err := h.DB.QueryContext(c.Request.Context(), `
		SELECT e.id, e.medication_id, m.name,
		       e.schedule_id, e.scheduled_at,
		       COALESCE(e.completed_at,''), e.status, e.confirmed_by_device
		FROM events e
		JOIN medications m ON m.id = e.medication_id
		WHERE e.scheduled_at >= datetime('now', '-' || ? || ' days')
		ORDER BY e.scheduled_at DESC`, days)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "query events: " + err.Error()})
		return
	}
	defer rows.Close()

	events := []Event{}
	for rows.Next() {
		var ev Event
		var sid sql.NullInt64
		if err := rows.Scan(&ev.ID, &ev.MedicationID, &ev.MedicationName,
			&sid, &ev.ScheduledAt, &ev.CompletedAt, &ev.Status, &ev.ConfirmedByDevice); err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "scan: " + err.Error()})
			return
		}
		if sid.Valid {
			ev.ScheduleID = &sid.Int64
		}
		events = append(events, ev)
	}
	c.JSON(http.StatusOK, events)
}

// GetStatus — GET /api/status
func (h *EventHandler) GetStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"deviceConnected": h.Hub.DeviceConnected(),
		"pendingCount":    h.Hub.PendingCount(),
	})
}

// DebugTrigger — POST /api/debug/trigger
// Fires the first active schedule immediately for demo/testing purposes.
func (h *EventHandler) DebugTrigger(c *gin.Context) {
	var medID, schedID int64
	var medName string
	err := h.DB.QueryRowContext(c.Request.Context(), `
		SELECT s.medication_id, s.id, m.name
		FROM schedules s
		JOIN medications m ON m.id = s.medication_id
		WHERE s.active = 1
		ORDER BY s.time_of_day
		LIMIT 1`).Scan(&medID, &schedID, &medName)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "no active schedules: " + err.Error()})
		return
	}

	eventID, err := h.Hub.TriggerDevice(medID, schedID, medName)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "trigger: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"eventId":        eventID,
		"medicationName": medName,
	})
}

// ─── Today Status ───────────────────────────────────────────────────────────

// TodayDose represents one expected dose for today, with its current status.
type TodayDose struct {
	ScheduleID     int64  `json:"scheduleId"`
	MedicationName string `json:"medicationName"`
	Dose           string `json:"dose"`
	TimeOfDay      string `json:"timeOfDay"`
	Status         string `json:"status"` // "upcoming", "pending", "completed", "missed", "due"
}

// TodayStatus — GET /api/today-status
// Computes today's expected doses from active schedules, cross-referenced with events.
func (h *EventHandler) TodayStatus(c *gin.Context) {
	now := time.Now()

	// ISO day-of-week: Monday=1 … Sunday=7.
	isoDow := int(now.Weekday())
	if isoDow == 0 {
		isoDow = 7 // Sunday
	}
	dowStr := strconv.Itoa(isoDow)

	// Query all active schedules that include today's day-of-week.
	rows, err := h.DB.QueryContext(c.Request.Context(), `
		SELECT s.id, m.name, m.dose, s.time_of_day, s.days_of_week, s.start_date, s.end_date
		FROM schedules s
		JOIN medications m ON m.id = s.medication_id
		WHERE s.active = 1
		ORDER BY s.time_of_day, m.name`)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "query schedules: " + err.Error()})
		return
	}
	defer rows.Close()

	todayDateStr := now.Format("2006-01-02")

	type schedRow struct {
		id      int64
		medName string
		dose    string
		tod     string
		dow     string
	}
	var todayScheds []schedRow
	for rows.Next() {
		var id int64
		var medName, dose, tod, dow, startDate string
		var endDate sql.NullString
		if err := rows.Scan(&id, &medName, &dose, &tod, &dow, &startDate, &endDate); err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "scan: " + err.Error()})
			return
		}

		// Filter by date range.
		if todayDateStr < startDate {
			continue // hasn't started yet
		}
		if endDate.Valid && endDate.String != "" && todayDateStr > endDate.String {
			continue // already ended
		}

		// Check if today's day-of-week is in the schedule's days_of_week list.
		days := strings.Split(dow, ",")
		for _, d := range days {
			if strings.TrimSpace(d) == dowStr {
				todayScheds = append(todayScheds, schedRow{id, medName, dose, tod, dow})
				break
			}
		}
	}

	// Query events created "today" based on SQLite's date normalization.
	// This avoids lexical datetime comparisons across mixed timestamp formats.
	eventRows, err := h.DB.QueryContext(c.Request.Context(), `
		SELECT schedule_id, status
		FROM events
		WHERE date(scheduled_at, 'localtime') = date('now', 'localtime')
		ORDER BY datetime(scheduled_at) DESC`)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "query events: " + err.Error()})
		return
	}
	defer eventRows.Close()

	// Map schedule_id → latest event status.
	eventMap := make(map[int64]string)
	for eventRows.Next() {
		var sid sql.NullInt64
		var status string
		if err := eventRows.Scan(&sid, &status); err != nil {
			continue
		}
		if sid.Valid {
			if _, exists := eventMap[sid.Int64]; !exists {
				eventMap[sid.Int64] = status // keep latest (query is DESC)
			}
		}
	}

	// Build the response.
	doses := make([]TodayDose, 0, len(todayScheds))
	for _, sr := range todayScheds {
		status := "upcoming"
		if evStatus, ok := eventMap[sr.id]; ok {
			status = evStatus // "pending", "completed", or "missed"
		} else {
			// No event yet — check if the scheduled time has passed.
			parts := strings.SplitN(sr.tod, ":", 2)
			if len(parts) == 2 {
				h, _ := strconv.Atoi(parts[0])
				m, _ := strconv.Atoi(parts[1])
				schedTime := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
				if now.After(schedTime) {
					status = "due" // past due but cron didn't fire (e.g. backend was off)
				}
			}
		}
		doses = append(doses, TodayDose{
			ScheduleID:     sr.id,
			MedicationName: sr.medName,
			Dose:           sr.dose,
			TimeOfDay:      sr.tod,
			Status:         status,
		})
	}

	c.JSON(http.StatusOK, doses)
}
