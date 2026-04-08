// med-reminder backend — main entrypoint.
//
// Serves REST API, WebSocket endpoints, and runs the cron scheduler.
// All state persists in SQLite at ./data/meds.db.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"med-reminder/backend/db"
	"med-reminder/backend/handlers"
	"med-reminder/backend/scheduler"

	"github.com/gin-gonic/gin"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// ── Database ────────────────────────────────────────────────────────
	database, err := db.Open("./data")
	if err != nil {
		log.Fatalf("db.Open: %v", err)
	}
	defer database.Close()

	// ── WebSocket Hub ───────────────────────────────────────────────────
	hub := handlers.NewHub(database)

	// ── Scheduler ───────────────────────────────────────────────────────
	sched := scheduler.New(database, hub)
	if err := sched.Start(); err != nil {
		log.Fatalf("scheduler.Start: %v", err)
	}
	defer sched.Stop()

	// ── Handlers ────────────────────────────────────────────────────────
	medH := &handlers.MedicationHandler{DB: database}
	schedH := &handlers.ScheduleHandler{DB: database, OnChange: func() {
		if err := sched.Reload(); err != nil {
			log.Printf("scheduler reload: %v", err)
		}
	}}
	eventH := &handlers.EventHandler{DB: database, Hub: hub}
	settingsH := &handlers.SettingsHandler{DB: database}

	// Let Hub read alert_duration from settings.
	hub.GetAlertDuration = settingsH.GetAlertDuration

	// ── Router ──────────────────────────────────────────────────────────
	r := gin.Default()

	// CORS middleware.
	r.Use(corsMiddleware())

	// REST API
	api := r.Group("/api")
	{
		api.GET("/medications", medH.ListMedications)
		api.POST("/medications", medH.CreateMedication)
		api.PUT("/medications/:id", medH.UpdateMedication)
		api.DELETE("/medications/:id", medH.DeleteMedication)

		api.GET("/schedules", schedH.ListSchedules)
		api.POST("/schedules", schedH.CreateSchedule)
		api.DELETE("/schedules/:id", schedH.DeleteSchedule)

		api.GET("/events", eventH.ListEvents)
		api.GET("/status", eventH.GetStatus)
		api.GET("/today-status", eventH.TodayStatus)
		api.POST("/debug/trigger", eventH.DebugTrigger)

		api.GET("/settings", settingsH.GetSettings)
		api.PUT("/settings", settingsH.UpdateSettings)
	}

	// WebSocket endpoints
	r.GET("/ws/caregiver", hub.HandleCaregiverWS)
	r.GET("/ws/device", hub.HandleDeviceWS)

	// ── Server ──────────────────────────────────────────────────────────
	addr := ":8080"
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		log.Printf("Received %v, shutting down …", sig)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("HTTP shutdown: %v", err)
		}
	}()

	log.Printf("Backend listening on %s", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("ListenAndServe: %v", err)
	}
	log.Println("Server stopped.")
}

// corsMiddleware adds CORS headers for the Vite dev server.
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
