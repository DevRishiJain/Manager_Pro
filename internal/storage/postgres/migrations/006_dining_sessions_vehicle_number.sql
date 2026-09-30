-- 006_dining_sessions_vehicle_number.sql
-- Add vehicle_number column to dining_sessions for drive-in ordering and venue support
ALTER TABLE dining_sessions ADD COLUMN IF NOT EXISTS vehicle_number VARCHAR(64) DEFAULT '';
