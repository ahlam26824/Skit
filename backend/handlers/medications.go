package handlers

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Medication represents a row in the medications table.
type Medication struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Dose      string `json:"dose"`
	Notes     string `json:"notes"`
	CreatedAt string `json:"createdAt"`
}

// MedicationInput is the JSON body for create/update.
type MedicationInput struct {
	Name  string `json:"name"`
	Dose  string `json:"dose"`
	Notes string `json:"notes"`
}

// MedicationHandler provides CRUD for the medications table.
type MedicationHandler struct {
	DB *sql.DB
}

// ListMedications — GET /api/medications
func (h *MedicationHandler) ListMedications(c *gin.Context) {
	rows, err := h.DB.QueryContext(c.Request.Context(),
		`SELECT id, name, COALESCE(dose,''), COALESCE(notes,''), created_at FROM medications ORDER BY id`)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "query medications: " + err.Error()})
		return
	}
	defer rows.Close()

	meds := []Medication{}
	for rows.Next() {
		var m Medication
		if err := rows.Scan(&m.ID, &m.Name, &m.Dose, &m.Notes, &m.CreatedAt); err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "scan: " + err.Error()})
			return
		}
		meds = append(meds, m)
	}
	c.JSON(http.StatusOK, meds)
}

// CreateMedication — POST /api/medications
func (h *MedicationHandler) CreateMedication(c *gin.Context) {
	var in MedicationInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid body: " + err.Error()})
		return
	}
	if in.Name == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	res, err := h.DB.ExecContext(c.Request.Context(),
		`INSERT INTO medications (name, dose, notes) VALUES (?, ?, ?)`,
		in.Name, in.Dose, in.Notes)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "insert: " + err.Error()})
		return
	}
	id, _ := res.LastInsertId()

	var m Medication
	h.DB.QueryRowContext(c.Request.Context(),
		`SELECT id, name, COALESCE(dose,''), COALESCE(notes,''), created_at FROM medications WHERE id=?`, id).
		Scan(&m.ID, &m.Name, &m.Dose, &m.Notes, &m.CreatedAt)

	c.JSON(http.StatusCreated, m)
}

// UpdateMedication — PUT /api/medications/{id}
func (h *MedicationHandler) UpdateMedication(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var in MedicationInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid body: " + err.Error()})
		return
	}

	result, err := h.DB.ExecContext(c.Request.Context(),
		`UPDATE medications SET name=?, dose=?, notes=? WHERE id=?`,
		in.Name, in.Dose, in.Notes, id)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "update: " + err.Error()})
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "medication not found"})
		return
	}

	var m Medication
	h.DB.QueryRowContext(c.Request.Context(),
		`SELECT id, name, COALESCE(dose,''), COALESCE(notes,''), created_at FROM medications WHERE id=?`, id).
		Scan(&m.ID, &m.Name, &m.Dose, &m.Notes, &m.CreatedAt)

	c.JSON(http.StatusOK, m)
}

// DeleteMedication — DELETE /api/medications/{id}
func (h *MedicationHandler) DeleteMedication(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	result, err := h.DB.ExecContext(c.Request.Context(), `DELETE FROM medications WHERE id=?`, id)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "delete: " + err.Error()})
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "medication not found"})
		return
	}
	c.Status(http.StatusNoContent)
}
