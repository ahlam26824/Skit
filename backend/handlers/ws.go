// // Package handlers implements WebSocket hub for caregiver + device connections.
// package handlers

// import (
// 	"database/sql"
// 	"encoding/json"
// 	"fmt"
// 	"log"
// 	"net/http"
// 	"sync"
// 	"time"

// 	"github.com/gin-gonic/gin"
// 	"github.com/gorilla/websocket"
// )

// // ─── Wire-protocol message types ────────────────────────────────────────────

// // DeviceHello is sent by the simulator on connect.
// type DeviceHello struct {
// 	Type     string `json:"type"`
// 	DeviceID string `json:"deviceId"`
// }

// // TriggerMsg is sent from backend → device (simplified: just alert duration).
// type TriggerMsg struct {
// 	Type     string `json:"type"`
// 	Duration int    `json:"duration"` // alert duration in minutes
// }

// // AckMsg is sent from device → backend (no eventId — backend resolves internally).
// type AckMsg struct {
// 	Type   string `json:"type"`
// 	Status string `json:"status"`
// }

// // CaregiverMsg is broadcast from backend → caregiver browsers.
// type CaregiverMsg struct {
// 	Type        string `json:"type"`
// 	EventID     int64  `json:"eventId"`
// 	ConfirmedAt string `json:"confirmedAt,omitempty"`
// 	MedName     string `json:"medicationName,omitempty"`
// 	ScheduledAt string `json:"scheduledAt,omitempty"`
// }

// // StatusMsg is sent to caregivers on connect and on device connect/disconnect.
// type StatusMsg struct {
// 	Type            string `json:"type"`
// 	DeviceConnected bool   `json:"deviceConnected"`
// }

// // ─── Hub ────────────────────────────────────────────────────────────────────

// // Hub manages WebSocket connections for caregivers and the IoT device.
// type Hub struct {
// 	db         *sql.DB
// 	caregivers map[*websocket.Conn]bool
// 	device     *websocket.Conn
// 	mu         sync.RWMutex

// 	// pendingTimers tracks cancel funcs for missed-timeout goroutines keyed by eventID.
// 	pendingTimers map[int64]func()
// 	timerMu       sync.Mutex

// 	// GetAlertDuration returns the alert duration in minutes from settings.
// 	GetAlertDuration func() int

// 	upgrader websocket.Upgrader
// }

// // NewHub creates a new WebSocket hub.
// func NewHub(db *sql.DB) *Hub {
// 	return &Hub{
// 		db:            db,
// 		caregivers:    make(map[*websocket.Conn]bool),
// 		pendingTimers: make(map[int64]func()),
// 		upgrader: websocket.Upgrader{
// 			CheckOrigin: func(r *http.Request) bool { return true }, // allow all origins for demo
// 		},
// 	}
// }

// // DeviceConnected reports whether the simulator is online.
// func (h *Hub) DeviceConnected() bool {
// 	h.mu.RLock()
// 	defer h.mu.RUnlock()
// 	return h.device != nil
// }

// // ─── Caregiver endpoint ─────────────────────────────────────────────────────

// // HandleCaregiverWS upgrades HTTP → WS for browser clients.
// func (h *Hub) HandleCaregiverWS(c *gin.Context) {
// 	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
// 	if err != nil {
// 		log.Printf("ws/caregiver upgrade: %v", err)
// 		return
// 	}

// 	h.mu.Lock()
// 	h.caregivers[conn] = true
// 	h.mu.Unlock()

// 	log.Printf("Caregiver connected (%d total)", len(h.caregivers))

// 	// Send current device status immediately.
// 	_ = conn.WriteJSON(StatusMsg{Type: "status", DeviceConnected: h.DeviceConnected()})

// 	// Keep connection alive; read loop discards client messages.
// 	defer func() {
// 		h.mu.Lock()
// 		delete(h.caregivers, conn)
// 		h.mu.Unlock()
// 		conn.Close()
// 		log.Println("Caregiver disconnected")
// 	}()

// 	for {
// 		if _, _, err := conn.ReadMessage(); err != nil {
// 			break
// 		}
// 	}
// }

// // ─── Device endpoint ────────────────────────────────────────────────────────

// // HandleDeviceWS upgrades HTTP → WS for the IoT simulator.
// func (h *Hub) HandleDeviceWS(c *gin.Context) {
// 	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
// 	if err != nil {
// 		log.Printf("ws/device upgrade: %v", err)
// 		return
// 	}

// 	log.Println("Device connected")

// 	h.mu.Lock()
// 	h.device = conn
// 	h.mu.Unlock()

// 	h.broadcastStatus()

// 	defer func() {
// 		h.mu.Lock()
// 		if h.device == conn {
// 			h.device = nil
// 		}
// 		h.mu.Unlock()
// 		conn.Close()
// 		log.Println("Device disconnected")
// 		h.broadcastStatus()
// 	}()

// 	for {
// 		_, raw, err := conn.ReadMessage()
// 		if err != nil {
// 			break
// 		}
// 		h.handleDeviceMessage(raw)
// 	}
// }

// func (h *Hub) handleDeviceMessage(raw []byte) {
// 	var base struct {
// 		Type string `json:"type"`
// 	}
// 	if err := json.Unmarshal(raw, &base); err != nil {
// 		log.Printf("device msg parse: %v", err)
// 		return
// 	}

// 	switch base.Type {
// 	case "hello":
// 		var hello DeviceHello
// 		json.Unmarshal(raw, &hello)
// 		log.Printf("Device identified: %s", hello.DeviceID)

// 	case "ack":
// 		var ack AckMsg
// 		if err := json.Unmarshal(raw, &ack); err != nil {
// 			log.Printf("ack parse: %v", err)
// 			return
// 		}
// 		h.handleAck(ack)

// 	default:
// 		log.Printf("unknown device message type: %s", base.Type)
// 	}
// }

// // handleAck processes a device acknowledgement.
// // Since the device doesn't know the eventId, we resolve the most recent pending event.
// func (h *Hub) handleAck(ack AckMsg) {
// 	now := time.Now().UTC().Format(time.RFC3339)

// 	// Find the most recent pending event.
// 	var eventID int64
// 	err := h.db.QueryRow(
// 		`SELECT id FROM events WHERE status='pending' ORDER BY scheduled_at DESC LIMIT 1`,
// 	).Scan(&eventID)
// 	if err != nil {
// 		log.Printf("ack: no pending event found: %v", err)
// 		return
// 	}

// 	status := "completed"
// 	if ack.Status == "missed" {
// 		status = "missed"
// 	}

// 	if status == "completed" {
// 		_, err = h.db.Exec(
// 			`UPDATE events SET status='completed', confirmed_by_device=1, completed_at=? WHERE id=?`,
// 			now, eventID,
// 		)
// 	} else {
// 		_, err = h.db.Exec(
// 			`UPDATE events SET status='missed' WHERE id=?`, eventID,
// 		)
// 	}
// 	if err != nil {
// 		log.Printf("ack db update: %v", err)
// 		return
// 	}

// 	// Cancel the missed-timeout goroutine.
// 	h.timerMu.Lock()
// 	if cancel, ok := h.pendingTimers[eventID]; ok {
// 		cancel()
// 		delete(h.pendingTimers, eventID)
// 	}
// 	h.timerMu.Unlock()

// 	log.Printf("Event %d marked %s (device ack)", eventID, status)

// 	if status == "completed" {
// 		h.BroadcastToCaregivers(CaregiverMsg{
// 			Type:        "completed",
// 			EventID:     eventID,
// 			ConfirmedAt: now,
// 		})
// 	} else {
// 		h.BroadcastToCaregivers(CaregiverMsg{
// 			Type:    "missed",
// 			EventID: eventID,
// 		})
// 	}
// }

// // ─── Trigger & Broadcast ───────────────────────────────────────────────────

// // TriggerDevice creates a pending event and sends a trigger to the device.
// // Returns the new event ID.
// func (h *Hub) TriggerDevice(medicationID, scheduleID int64, medName string) (int64, error) {
// 	now := time.Now().UTC().Format(time.RFC3339)

// 	res, err := h.db.Exec(
// 		`INSERT INTO events (medication_id, schedule_id, scheduled_at, status) VALUES (?, ?, ?, 'pending')`,
// 		medicationID, scheduleID, now,
// 	)
// 	if err != nil {
// 		return 0, fmt.Errorf("insert event: %w", err)
// 	}
// 	eventID, _ := res.LastInsertId()

// 	h.mu.RLock()
// 	dev := h.device
// 	h.mu.RUnlock()

// 	duration := 5
// 	if h.GetAlertDuration != nil {
// 		duration = h.GetAlertDuration()
// 	}

// 	if dev != nil {
// 		msg := TriggerMsg{Type: "trigger", Duration: duration}
// 		h.mu.Lock()
// 		err = dev.WriteJSON(msg)
// 		h.mu.Unlock()
// 		if err != nil {
// 			log.Printf("send trigger to device: %v", err)
// 		}
// 	} else {
// 		log.Println("No device connected — trigger queued as pending")
// 	}

// 	// Notify caregivers about the trigger.
// 	h.BroadcastToCaregivers(CaregiverMsg{
// 		Type:        "trigger",
// 		EventID:     eventID,
// 		MedName:     medName,
// 		ScheduledAt: now,
// 	})

// 	// Start missed-timeout goroutine using the settings-based duration.
// 	timeoutMin := duration
// 	if timeoutMin <= 0 {
// 		timeoutMin = 5
// 	}
// 	h.startMissedTimer(eventID, time.Duration(timeoutMin)*time.Minute)

// 	return eventID, nil
// }

// // startMissedTimer starts a goroutine that marks the event as missed after the
// // given duration unless cancelled (i.e., ack received).
// func (h *Hub) startMissedTimer(eventID int64, timeout time.Duration) {
// 	done := make(chan struct{})
// 	cancel := func() { close(done) }

// 	h.timerMu.Lock()
// 	h.pendingTimers[eventID] = cancel
// 	h.timerMu.Unlock()

// 	go func() {
// 		select {
// 		case <-done:
// 			return // ack received, timer cancelled
// 		case <-time.After(timeout):
// 			h.BroadcastMissed(eventID)
// 			h.timerMu.Lock()
// 			delete(h.pendingTimers, eventID)
// 			h.timerMu.Unlock()
// 		}
// 	}()
// }

// // BroadcastMissed marks an event as missed in the DB and broadcasts to caregivers.
// func (h *Hub) BroadcastMissed(eventID int64) {
// 	_, err := h.db.Exec(`UPDATE events SET status='missed' WHERE id=? AND status='pending'`, eventID)
// 	if err != nil {
// 		log.Printf("missed db update: %v", err)
// 		return
// 	}

// 	log.Printf("Event %d marked as missed", eventID)

// 	h.BroadcastToCaregivers(CaregiverMsg{
// 		Type:    "missed",
// 		EventID: eventID,
// 	})
// }

// // BroadcastToCaregivers sends a JSON message to all connected caregiver browsers.
// func (h *Hub) BroadcastToCaregivers(msg interface{}) {
// 	h.mu.RLock()
// 	defer h.mu.RUnlock()

// 	for conn := range h.caregivers {
// 		if err := conn.WriteJSON(msg); err != nil {
// 			log.Printf("broadcast to caregiver: %v", err)
// 		}
// 	}
// }

// func (h *Hub) broadcastStatus() {
// 	h.BroadcastToCaregivers(StatusMsg{
// 		Type:            "status",
// 		DeviceConnected: h.DeviceConnected(),
// 	})
// }

// // PendingCount returns the number of events with status='pending'.
// func (h *Hub) PendingCount() int {
// 	var count int
// 	h.db.QueryRow(`SELECT COUNT(*) FROM events WHERE status='pending'`).Scan(&count)
// 	return count
// }



// Package handlers implements WebSocket hub for caregiver + device connections.
package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// ─── Wire-protocol message types ────────────────────────────────────────────

// DeviceHello is sent by the simulator on connect.
type DeviceHello struct {
	Type     string `json:"type"`
	DeviceID string `json:"deviceId"`
}

// TriggerMsg is sent from backend → device (simplified: just alert duration).
type TriggerMsg struct {
	Type     string `json:"type"`
	Duration int    `json:"duration"` // alert duration in minutes
}

// AckMsg is sent from device → backend (no eventId — backend resolves internally).
type AckMsg struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

// CaregiverMsg is broadcast from backend → caregiver browsers.
type CaregiverMsg struct {
	Type        string `json:"type"`
	EventID     int64  `json:"eventId"`
	ConfirmedAt string `json:"confirmedAt,omitempty"`
	MedName     string `json:"medicationName,omitempty"`
	ScheduledAt string `json:"scheduledAt,omitempty"`
}

// StatusMsg is sent to caregivers on connect and on device connect/disconnect.
type StatusMsg struct {
	Type            string `json:"type"`
	DeviceConnected bool   `json:"deviceConnected"`
}

// ─── Hub ────────────────────────────────────────────────────────────────────

// Hub manages WebSocket connections for caregivers and the IoT device.
// The backend no longer runs its own missed-timeout timer — the ESP32 is the
// sole authority on completed vs missed. The backend simply reacts to acks.
type Hub struct {
	db         *sql.DB
	caregivers map[*websocket.Conn]bool
	device     *websocket.Conn
	mu         sync.RWMutex

	// GetAlertDuration returns the alert duration in minutes from settings.
	GetAlertDuration func() int

	upgrader websocket.Upgrader
}

// NewHub creates a new WebSocket hub.
func NewHub(db *sql.DB) *Hub {
	return &Hub{
		db:         db,
		caregivers: make(map[*websocket.Conn]bool),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// DeviceConnected reports whether the device is online.
func (h *Hub) DeviceConnected() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.device != nil
}

// ─── Caregiver endpoint ─────────────────────────────────────────────────────

// HandleCaregiverWS upgrades HTTP → WS for browser clients.
func (h *Hub) HandleCaregiverWS(c *gin.Context) {
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("ws/caregiver upgrade: %v", err)
		return
	}

	h.mu.Lock()
	h.caregivers[conn] = true
	h.mu.Unlock()

	log.Printf("Caregiver connected (%d total)", len(h.caregivers))

	// Send current device status immediately.
	_ = conn.WriteJSON(StatusMsg{Type: "status", DeviceConnected: h.DeviceConnected()})

	defer func() {
		h.mu.Lock()
		delete(h.caregivers, conn)
		h.mu.Unlock()
		conn.Close()
		log.Println("Caregiver disconnected")
	}()

	// Keep connection alive; read loop discards client messages.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
}

// ─── Device endpoint ────────────────────────────────────────────────────────

// HandleDeviceWS upgrades HTTP → WS for the ESP32 device.
func (h *Hub) HandleDeviceWS(c *gin.Context) {
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("ws/device upgrade: %v", err)
		return
	}

	log.Println("Device connected")

	h.mu.Lock()
	h.device = conn
	h.mu.Unlock()

	h.broadcastStatus()

	defer func() {
		h.mu.Lock()
		if h.device == conn {
			h.device = nil
		}
		h.mu.Unlock()
		conn.Close()
		log.Println("Device disconnected")
		h.broadcastStatus()
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		h.handleDeviceMessage(raw)
	}
}

func (h *Hub) handleDeviceMessage(raw []byte) {
	var base struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &base); err != nil {
		log.Printf("device msg parse: %v", err)
		return
	}

	switch base.Type {
	case "hello":
		var hello DeviceHello
		json.Unmarshal(raw, &hello)
		log.Printf("Device identified: %s", hello.DeviceID)

	case "ack":
		var ack AckMsg
		if err := json.Unmarshal(raw, &ack); err != nil {
			log.Printf("ack parse: %v", err)
			return
		}
		h.handleAck(ack)

	default:
		log.Printf("unknown device message type: %s", base.Type)
	}
}

// handleAck processes a device acknowledgement.
// The ESP32 is the sole authority on completed vs missed —
// the backend no longer runs its own timeout timer.
func (h *Hub) handleAck(ack AckMsg) {
	now := time.Now().UTC().Format(time.RFC3339)

	// Find the most recent pending event.
	var eventID int64
	err := h.db.QueryRow(
		`SELECT id FROM events WHERE status='pending' ORDER BY scheduled_at DESC LIMIT 1`,
	).Scan(&eventID)
	if err != nil {
		log.Printf("ack: no pending event found: %v", err)
		return
	}

	status := "completed"
	if ack.Status == "missed" {
		status = "missed"
	}

	if status == "completed" {
		_, err = h.db.Exec(
			`UPDATE events SET status='completed', confirmed_by_device=1, completed_at=? WHERE id=?`,
			now, eventID,
		)
	} else {
		_, err = h.db.Exec(
			`UPDATE events SET status='missed' WHERE id=?`, eventID,
		)
	}
	if err != nil {
		log.Printf("ack db update: %v", err)
		return
	}

	log.Printf("Event %d marked %s (device ack)", eventID, status)

	if status == "completed" {
		h.BroadcastToCaregivers(CaregiverMsg{
			Type:        "completed",
			EventID:     eventID,
			ConfirmedAt: now,
		})
	} else {
		h.BroadcastToCaregivers(CaregiverMsg{
			Type:    "missed",
			EventID: eventID,
		})
	}
}

// ─── Trigger & Broadcast ────────────────────────────────────────────────────

// TriggerDevice creates a pending event and sends a trigger to the device.
// Returns the new event ID.
// NOTE: No missed-timeout goroutine is started here. The ESP32 handles its own
// timeout and will send ack:"missed" when the duration expires on-device.
func (h *Hub) TriggerDevice(medicationID, scheduleID int64, medName string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	res, err := h.db.Exec(
		`INSERT INTO events (medication_id, schedule_id, scheduled_at, status) VALUES (?, ?, ?, 'pending')`,
		medicationID, scheduleID, now,
	)
	if err != nil {
		return 0, fmt.Errorf("insert event: %w", err)
	}
	eventID, _ := res.LastInsertId()

	h.mu.RLock()
	dev := h.device
	h.mu.RUnlock()

	duration := 5
	if h.GetAlertDuration != nil {
		duration = h.GetAlertDuration()
	}

	if dev != nil {
		msg := TriggerMsg{Type: "trigger", Duration: duration}
		h.mu.Lock()
		err = dev.WriteJSON(msg)
		h.mu.Unlock()
		if err != nil {
			log.Printf("send trigger to device: %v", err)
		}
	} else {
		log.Println("No device connected — trigger queued as pending")
	}

	// Notify caregivers about the new trigger.
	h.BroadcastToCaregivers(CaregiverMsg{
		Type:        "trigger",
		EventID:     eventID,
		MedName:     medName,
		ScheduledAt: now,
	})

	// No backend timer started — ESP32 owns the timeout.

	return eventID, nil
}

// BroadcastToCaregivers sends a JSON message to all connected caregiver browsers.
func (h *Hub) BroadcastToCaregivers(msg interface{}) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for conn := range h.caregivers {
		if err := conn.WriteJSON(msg); err != nil {
			log.Printf("broadcast to caregiver: %v", err)
		}
	}
}

func (h *Hub) broadcastStatus() {
	h.BroadcastToCaregivers(StatusMsg{
		Type:            "status",
		DeviceConnected: h.DeviceConnected(),
	})
}

// PendingCount returns the number of events with status='pending'.
func (h *Hub) PendingCount() int {
	var count int
	h.db.QueryRow(`SELECT COUNT(*) FROM events WHERE status='pending'`).Scan(&count)
	return count
}