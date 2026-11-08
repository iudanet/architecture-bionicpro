"""ETL витрины отчётов BionicPRO.

CRM (Postgres) и сырая телеметрия протезов (Postgres) -> ClickHouse -> витрина report_user_daily.

Инкремент: по watermark в bionicpro.etl_watermark. Каждый запуск забирает события телеметрии,
принятые сервером в окне (loaded_until; data_interval_end], пересобирает витрину за затронутые
дни и сдвигает watermark. Сервис отчётов отдаёт данные только до loaded_until.
"""

from __future__ import annotations

import logging
import os
from datetime import date, datetime, timedelta, timezone
from pathlib import Path

import clickhouse_connect
from airflow import DAG
from airflow.operators.python import PythonOperator
from airflow.providers.postgres.hooks.postgres import PostgresHook

PIPELINE = "reports_mart"
EPOCH = datetime(1970, 1, 1, tzinfo=timezone.utc)
BATCH_SIZE = 50_000
SQL_DIR = Path(__file__).parent / "sql"

log = logging.getLogger(__name__)

CRM_QUERY = """
    SELECT p.serial, p.model, p.issued_at, c.sso_user_id::text, c.full_name, c.country
    FROM prostheses p
    JOIN clients c ON c.id = p.client_id
"""

TELEMETRY_QUERY = """
    SELECT event_id, prosthesis_serial, ts, received_at, movement_type,
           reaction_ms, battery_pct::float8, error_code
    FROM telemetry_events
    WHERE received_at > %(lower)s AND received_at <= %(upper)s
    ORDER BY received_at, event_id
"""

TELEMETRY_COLUMNS = [
    "event_id", "prosthesis_serial", "ts", "received_at",
    "movement_type", "reaction_ms", "battery_pct", "error_code",
]


def clickhouse():
    """Клиент ClickHouse под пользователем ETL."""
    return clickhouse_connect.get_client(
        host=os.environ["CLICKHOUSE_HOST"],
        port=int(os.environ.get("CLICKHOUSE_PORT", "8123")),
        username=os.environ["CLICKHOUSE_USER"],
        password=os.environ["CLICKHOUSE_PASSWORD"],
        database="bionicpro",
    )


def get_watermark(ch) -> datetime:
    rows = ch.query(
        "SELECT loaded_until FROM bionicpro.etl_watermark FINAL WHERE pipeline = {p:String}",
        parameters={"p": PIPELINE},
    ).result_rows
    if not rows:
        return EPOCH
    value = rows[0][0]
    return value if value.tzinfo else value.replace(tzinfo=timezone.utc)


def extract_crm_to_olap(**_):
    """Снимок клиентов и протезов из CRM: в OLAP уходят только поля, нужные отчёту."""
    rows = PostgresHook(postgres_conn_id="crm_db").get_records(CRM_QUERY)
    synced_at = datetime.now(timezone.utc)
    data = [(*row, synced_at) for row in rows]
    clickhouse().insert(
        "bionicpro.crm_prostheses",
        data,
        column_names=[
            "prosthesis_serial", "prosthesis_model", "issued_at",
            "user_id", "full_name", "country", "synced_at",
        ],
    )
    log.info("crm_prostheses: synced %s rows", len(data))


def extract_telemetry_to_olap(data_interval_end, **_):
    """Телеметрия, принятая после watermark, -> telemetry_raw. Возвращает окно и затронутые даты."""
    ch = clickhouse()
    lower = get_watermark(ch)
    upper = data_interval_end
    if upper <= lower:
        log.info("nothing to do: upper %s <= watermark %s", upper, lower)
        return {"upper": lower.isoformat(), "date_from": None, "date_to": None}

    conn = PostgresHook(postgres_conn_id="telemetry_db").get_conn()
    total, date_from, date_to = 0, None, None
    try:
        # Серверный курсор: события читаются пачками, а не целиком в память.
        with conn.cursor(name="telemetry_extract") as cur:
            cur.itersize = BATCH_SIZE
            cur.execute(TELEMETRY_QUERY, {"lower": lower, "upper": upper})
            while batch := cur.fetchmany(BATCH_SIZE):
                ch.insert("bionicpro.telemetry_raw", batch, column_names=TELEMETRY_COLUMNS)
                days = {row[2].astimezone(timezone.utc).date() for row in batch}
                date_from = min(days | ({date_from} if date_from else set()))
                date_to = max(days | ({date_to} if date_to else set()))
                total += len(batch)
    finally:
        conn.close()

    log.info("telemetry_raw: loaded %s events received in (%s, %s]", total, lower, upper)
    return {
        "upper": upper.isoformat(),
        "date_from": date_from.isoformat() if date_from else None,
        "date_to": date_to.isoformat() if date_to else None,
    }


def build_report_mart(ti, **_):
    """Пересобирает витрину report_user_daily за дни, в которые пришли новые события."""
    window = ti.xcom_pull(task_ids="extract_telemetry_to_olap")
    if not window or not window["date_from"]:
        log.info("no new telemetry, mart is up to date")
        return
    ch = clickhouse()
    ch.command(
        (SQL_DIR / "build_report_mart.sql").read_text(),
        parameters={
            "date_from": date.fromisoformat(window["date_from"]),
            "date_to": date.fromisoformat(window["date_to"]),
        },
    )
    log.info("report_user_daily: rebuilt %s .. %s", window["date_from"], window["date_to"])


def update_watermark(ti, **_):
    """Фиксирует, до какого момента данные обработаны: только после успешной сборки витрины."""
    window = ti.xcom_pull(task_ids="extract_telemetry_to_olap")
    upper = datetime.fromisoformat(window["upper"])
    clickhouse().insert(
        "bionicpro.etl_watermark",
        [(PIPELINE, upper, datetime.now(timezone.utc))],
        column_names=["pipeline", "loaded_until", "updated_at"],
    )
    log.info("watermark %s = %s", PIPELINE, upper)


with DAG(
    dag_id="bionicpro_reports_etl",
    description="CRM + телеметрия -> ClickHouse, витрина report_user_daily для сервиса отчётов",
    schedule=os.environ.get("REPORTS_ETL_SCHEDULE", "@hourly"),
    start_date=datetime(2026, 11, 1, tzinfo=timezone.utc),
    catchup=False,  # история догружается по watermark, а не прогоном пропущенных интервалов
    max_active_runs=1,  # окна не должны пересекаться
    default_args={
        "owner": "data-platform",
        "retries": 2,
        "retry_delay": timedelta(minutes=1),
    },
    tags=["bionicpro", "reports"],
) as dag:
    extract_crm = PythonOperator(task_id="extract_crm_to_olap", python_callable=extract_crm_to_olap)
    extract_telemetry = PythonOperator(
        task_id="extract_telemetry_to_olap", python_callable=extract_telemetry_to_olap
    )
    build_mart = PythonOperator(task_id="build_report_mart", python_callable=build_report_mart)
    watermark = PythonOperator(task_id="update_watermark", python_callable=update_watermark)

    [extract_crm, extract_telemetry] >> build_mart >> watermark
