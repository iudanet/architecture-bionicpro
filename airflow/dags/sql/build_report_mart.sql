INSERT INTO bionicpro.report_user_daily
SELECT
    p.user_id,
    p.full_name,
    p.country,
    t.prosthesis_serial,
    p.prosthesis_model,
    toDate(t.ts)                         AS date,
    count()                              AS movements,
    avg(t.reaction_ms)                   AS avg_reaction_ms,
    quantile(0.95)(t.reaction_ms)        AS p95_reaction_ms,
    max(t.reaction_ms)                   AS max_reaction_ms,
    countIf(t.reaction_ms > 100)         AS slow_reactions,
    countIf(t.error_code IS NOT NULL)    AS errors,
    avg(t.battery_pct)                   AS avg_battery_pct,
    min(t.battery_pct)                   AS min_battery_pct,
    now64(3, 'UTC')                      AS built_at
FROM bionicpro.telemetry_raw AS t FINAL
INNER JOIN
(
    SELECT * FROM bionicpro.crm_prostheses FINAL
) AS p ON p.prosthesis_serial = t.prosthesis_serial
WHERE toDate(t.ts) BETWEEN {date_from:Date} AND {date_to:Date}
GROUP BY p.user_id, p.full_name, p.country, t.prosthesis_serial, p.prosthesis_model, date
