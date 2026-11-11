#!/bin/sh
set -eu
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d telemetry <<'SQL'
CREATE TABLE telemetry_events (
    event_id          BIGSERIAL PRIMARY KEY,
    prosthesis_serial TEXT         NOT NULL,
    ts                TIMESTAMPTZ  NOT NULL,              -- время события на протезе
    received_at       TIMESTAMPTZ  NOT NULL DEFAULT now(), -- время приёма на сервере (по нему инкремент ETL)
    movement_type     TEXT         NOT NULL,
    reaction_ms       INTEGER      NOT NULL,
    battery_pct       NUMERIC(5,2) NOT NULL,
    error_code        TEXT
);
CREATE INDEX telemetry_events_received_at_idx ON telemetry_events (received_at);

-- История за 14 дней: событие раз в 15 минут по каждому протезу с включённой телеметрией.
INSERT INTO telemetry_events (prosthesis_serial, ts, received_at, movement_type, reaction_ms, battery_pct, error_code)
SELECT s.serial,
       t,
       t + interval '2 seconds',
       (ARRAY['grip', 'release', 'pinch', 'rotate', 'step'])[1 + floor(random() * 5)::int],
       (s.base_ms + random() * 60)::int,
       round((100 - extract(hour FROM t) * 3 - random() * 10)::numeric, 2),
       CASE WHEN random() < 0.02 THEN 'E_SENSOR_NOISE' END
FROM (VALUES ('BP-ARM-0001', 55), ('BP-ARM-0002', 70), ('BP-LEG-0003', 45)) AS s(serial, base_ms),
     generate_series(date_trunc('hour', now()) - interval '14 days', now() - interval '1 hour', interval '15 minutes') AS t;

GRANT CONNECT ON DATABASE telemetry TO telemetry_reader, telemetry_writer;
GRANT USAGE ON SCHEMA public TO telemetry_reader, telemetry_writer;
GRANT SELECT ON telemetry_events TO telemetry_reader;
GRANT INSERT ON telemetry_events TO telemetry_writer;
GRANT USAGE ON SEQUENCE telemetry_events_event_id_seq TO telemetry_writer;
SQL
