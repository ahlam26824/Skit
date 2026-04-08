-- schema.sql — med-reminder database schema
-- Applied automatically by db.go on startup.

CREATE TABLE IF NOT EXISTS medications (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    dose       TEXT NOT NULL DEFAULT '',
    notes      TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS schedules (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    medication_id INTEGER NOT NULL REFERENCES medications(id) ON DELETE CASCADE,
    time_of_day   TEXT NOT NULL,   -- "HH:MM" 24-hour format
    days_of_week  TEXT NOT NULL,   -- comma-separated: "1,2,3,4,5,6,7"
    start_date    TEXT NOT NULL DEFAULT '2000-01-01', -- "YYYY-MM-DD"
    end_date      TEXT,            -- "YYYY-MM-DD" or NULL for indefinite
    active        BOOLEAN DEFAULT 1
);

CREATE TABLE IF NOT EXISTS events (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    medication_id       INTEGER NOT NULL REFERENCES medications(id),
    schedule_id         INTEGER REFERENCES schedules(id),
    scheduled_at        DATETIME NOT NULL,
    completed_at        DATETIME,
    status              TEXT NOT NULL CHECK(status IN ('pending','completed','missed')),
    confirmed_by_device BOOLEAN DEFAULT 0
);

CREATE TABLE IF NOT EXISTS caregivers (
    id    INTEGER PRIMARY KEY AUTOINCREMENT,
    name  TEXT NOT NULL,
    email TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Default settings (inserted only if not present).
INSERT OR IGNORE INTO settings (key, value) VALUES ('patient_name', 'Name');
INSERT OR IGNORE INTO settings (key, value) VALUES ('patient_type', '');
INSERT OR IGNORE INTO settings (key, value) VALUES ('alert_duration', '5');
