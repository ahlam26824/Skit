package handlers

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// SettingsMap is the JSON response for GET /api/settings.
type SettingsMap map[string]string

// SettingsHandler provides endpoints for the settings table.
type SettingsHandler struct {
	DB *sql.DB
}

// GetSettings — GET /api/settings
func (h *SettingsHandler) GetSettings(c *gin.Context) {
	rows, err := h.DB.QueryContext(c.Request.Context(), `SELECT key, value FROM settings`)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "query: " + err.Error()})
		return
	}
	defer rows.Close()

	out := SettingsMap{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		out[k] = v
	}
	c.JSON(http.StatusOK, out)
}

// UpdateSettings — PUT /api/settings
// Accepts a JSON object of key-value pairs to upsert.
func (h *SettingsHandler) UpdateSettings(c *gin.Context) {
	var body map[string]string
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid body: " + err.Error()})
		return
	}

	for k, v := range body {
		_, err := h.DB.ExecContext(c.Request.Context(),
			`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=?`, k, v, v)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "upsert " + k + ": " + err.Error()})
			return
		}
	}

	// Return updated settings.
	h.GetSettings(c)
}

// GetAlertDuration reads the alert_duration setting (returns minutes as int, default 5).
func (h *SettingsHandler) GetAlertDuration() int {
	var val string
	err := h.DB.QueryRow(`SELECT value FROM settings WHERE key='alert_duration'`).Scan(&val)
	if err != nil {
		return 5
	}
	n, err := strconv.Atoi(val)
	if err != nil || n <= 0 {
		return 5
	}
	return n
}
