package handlers

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Schedule represents a schedule row joined with the medication name.
type Schedule struct {
	ID             int64   `json:"id"`
	MedicationID   int64   `json:"medicationId"`
	MedicationName string  `json:"medicationName"`
	TimeOfDay      string  `json:"timeOfDay"`
	DaysOfWeek     string  `json:"daysOfWeek"`
	StartDate      string  `json:"startDate"`
	EndDate        *string `json:"endDate"` // nil = indefinite
	Active         bool    `json:"active"`
}

// ScheduleInput is the JSON body for creating schedule(s).
// Accepts multiple times — one schedule row per time.
type ScheduleInput struct {
	MedicationID int64    `json:"medicationId"`
	Times        []string `json:"times"`      // ["08:00","14:00","20:00"]
	TimeOfDay    string   `json:"timeOfDay"`   // legacy single-time (fallback)
	DaysOfWeek   string   `json:"daysOfWeek"`
	StartDate    string   `json:"startDate"`   // "YYYY-MM-DD"
	EndDate      *string  `json:"endDate"`     // "YYYY-MM-DD" or null
}

// ScheduleHandler provides endpoints for the schedules table.
type ScheduleHandler struct {
	DB       *sql.DB
	OnChange func() // called after writes to reload the cron scheduler
}

// ListSchedules — GET /api/schedules
func (h *ScheduleHandler) ListSchedules(c *gin.Context) {
	rows, err := h.DB.QueryContext(c.Request.Context(), `
		SELECT s.id, s.medication_id, m.name, s.time_of_day, s.days_of_week,
		       s.start_date, s.end_date, s.active
		FROM schedules s
		JOIN medications m ON m.id = s.medication_id
		ORDER BY s.time_of_day, m.name`)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "query schedules: " + err.Error()})
		return
	}
	defer rows.Close()

	scheds := []Schedule{}
	for rows.Next() {
		var s Schedule
		var endDate sql.NullString
		if err := rows.Scan(&s.ID, &s.MedicationID, &s.MedicationName,
			&s.TimeOfDay, &s.DaysOfWeek, &s.StartDate, &endDate, &s.Active); err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "scan: " + err.Error()})
			return
		}
		if endDate.Valid {
			s.EndDate = &endDate.String
		}
		scheds = append(scheds, s)
	}
	c.JSON(http.StatusOK, scheds)
}

// CreateSchedule — POST /api/schedules
// Accepts multiple times — creates one schedule row per time.
// Returns an array of created schedules.
func (h *ScheduleHandler) CreateSchedule(c *gin.Context) {
	var in ScheduleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid body: " + err.Error()})
		return
	}

	// Resolve times list: prefer "times" array, fall back to single "timeOfDay".
	times := in.Times
	if len(times) == 0 && in.TimeOfDay != "" {
		times = []string{in.TimeOfDay}
	}

	if in.MedicationID == 0 || len(times) == 0 || in.DaysOfWeek == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "medicationId, times/timeOfDay, daysOfWeek required"})
		return
	}

	if in.StartDate == "" {
		in.StartDate = "2000-01-01"
	}

	created := []Schedule{}
	for _, tod := range times {
		var endDate interface{} = nil
		if in.EndDate != nil && *in.EndDate != "" {
			endDate = *in.EndDate
		}

		res, err := h.DB.ExecContext(c.Request.Context(),
			`INSERT INTO schedules (medication_id, time_of_day, days_of_week, start_date, end_date, active)
			 VALUES (?, ?, ?, ?, ?, 1)`,
			in.MedicationID, tod, in.DaysOfWeek, in.StartDate, endDate)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "insert: " + err.Error()})
			return
		}
		id, _ := res.LastInsertId()

		var s Schedule
		var ed sql.NullString
		h.DB.QueryRowContext(c.Request.Context(), `
			SELECT s.id, s.medication_id, m.name, s.time_of_day, s.days_of_week,
			       s.start_date, s.end_date, s.active
			FROM schedules s JOIN medications m ON m.id = s.medication_id
			WHERE s.id=?`, id).
			Scan(&s.ID, &s.MedicationID, &s.MedicationName,
				&s.TimeOfDay, &s.DaysOfWeek, &s.StartDate, &ed, &s.Active)
		if ed.Valid {
			s.EndDate = &ed.String
		}
		created = append(created, s)
	}

	if h.OnChange != nil {
		h.OnChange()
	}

	c.JSON(http.StatusCreated, created)
}

// DeleteSchedule — DELETE /api/schedules/{id}
func (h *ScheduleHandler) DeleteSchedule(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	// Manually cascade delete events associated with this schedule
	// to avoid foreign key constraint errors.
	if _, err := h.DB.ExecContext(c.Request.Context(), `DELETE FROM events WHERE schedule_id=?`, id); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "delete associated events: " + err.Error()})
		return
	}

	result, err := h.DB.ExecContext(c.Request.Context(), `DELETE FROM schedules WHERE id=?`, id)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "delete schedule: " + err.Error()})
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "schedule not found"})
		return
	}

	if h.OnChange != nil {
		h.OnChange()
	}

	c.Status(http.StatusNoContent)
}
