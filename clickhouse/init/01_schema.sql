CREATE DATABASE IF NOT EXISTS bionicpro;

-- Измерение из CRM: только поля, нужные отчёту (без телефона, адреса и т. п.).
CREATE TABLE IF NOT EXISTS bionicpro.crm_prostheses
(
    prosthesis_serial String,
    prosthesis_model  LowCardinality(String),
    issued_at         Date,
    user_id           String,                 -- sub пользователя в IdP
    full_name         String,
    country           LowCardinality(String),
    synced_at         DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree(synced_at)
ORDER BY prosthesis_serial;

-- Сырые события телеметрии. Ключ включает event_id, поэтому повторная загрузка того же окна идемпотентна.
CREATE TABLE IF NOT EXISTS bionicpro.telemetry_raw
(
    event_id          UInt64,
    prosthesis_serial String,
    ts                DateTime64(3, 'UTC'),
    received_at       DateTime64(3, 'UTC'),
    movement_type     LowCardinality(String),
    reaction_ms       UInt32,
    battery_pct       Float32,
    error_code        Nullable(String)
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (prosthesis_serial, ts, event_id);

-- Витрина для сервиса отчётов: телеметрия, агрегированная по дням в разрезе клиента и протеза.
-- Первичный ключ начинается с user_id: запрос отчёта по пользователю читает только его гранулы.
CREATE TABLE IF NOT EXISTS bionicpro.report_user_daily
(
    user_id           String,
    full_name         String,
    country           LowCardinality(String),
    prosthesis_serial String,
    prosthesis_model  LowCardinality(String),
    date              Date,
    movements         UInt64,
    avg_reaction_ms   Float64,
    p95_reaction_ms   Float64,
    max_reaction_ms   UInt32,
    slow_reactions    UInt64,                 -- реакции дольше 100 мс
    errors            UInt64,
    avg_battery_pct   Float64,
    min_battery_pct   Float32,
    built_at          DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree(built_at)
PARTITION BY toYYYYMM(date)
ORDER BY (user_id, prosthesis_serial, date);

-- До какого момента данные обработаны ETL. Сервис отчётов не отдаёт период позже loaded_until.
CREATE TABLE IF NOT EXISTS bionicpro.etl_watermark
(
    pipeline     LowCardinality(String),
    loaded_until DateTime64(3, 'UTC'),
    updated_at   DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree(updated_at)
ORDER BY pipeline;
