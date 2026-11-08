#!/bin/sh
# Имитация realtime-телеметрии с протезов (4G): раз в TELEMETRY_INTERVAL секунд пишет событие
# для случайного протеза с включённой телеметрией.
set -eu
interval="${TELEMETRY_INTERVAL:-10}"
until pg_isready -q; do sleep 1; done
while true; do
  psql -q -v ON_ERROR_STOP=1 <<'SQL' || echo "telemetry insert failed" >&2
INSERT INTO telemetry_events (prosthesis_serial, ts, movement_type, reaction_ms, battery_pct, error_code)
SELECT (ARRAY['BP-ARM-0001', 'BP-ARM-0002', 'BP-LEG-0003'])[1 + floor(random() * 3)::int],
       now(),
       (ARRAY['grip', 'release', 'pinch', 'rotate', 'step'])[1 + floor(random() * 5)::int],
       (40 + random() * 90)::int,
       round((20 + random() * 80)::numeric, 2),
       CASE WHEN random() < 0.02 THEN 'E_SENSOR_NOISE' END;
SQL
  sleep "$interval"
done
