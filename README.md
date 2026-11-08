# architecture-bionicpro

Проектная работа 9 спринта: BionicPRO, SSO и сервис отчётов.

| Задание | Содержание |
|---|---|
| [Task1](./Task1) | Безопасность: C4 to-be (брокер Keycloak, региональные IdP, BFF), PKCE вместо Code Grant |
| [Task2](./Task2) | Сервис отчётов: архитектура, Airflow DAG, витрина в ClickHouse, API `/reports`, ограничение доступа, кнопка в UI |

## Состав репозитория

| Каталог | Что внутри |
|---|---|
| `keycloak/` | realm `reports-realm`: клиент `reports-bff` (confidential, PKCE S256), `reports-api` (bearer-only), пользователи |
| `backend/` | Go: `cmd/bff` — Backend-for-Frontend (OAuth2 Code + PKCE, серверные сессии), `cmd/reports` — сервис отчётов |
| `frontend/` | React SPA + nginx (проксирует `/bff/*` на BFF) |
| `airflow/` | Airflow: Dockerfile, DAG `bionicpro_reports_etl`, SQL витрины |
| `clickhouse/` | DDL OLAP-хранилища и пользователи ClickHouse |
| `sources/` | Источники: Postgres CRM и телеметрии, симулятор realtime-телеметрии |

## Запуск

```bash
docker compose up -d --build
```

Приложение: http://localhost:3000, `prothetic1` / `prothetic123`. Остальное — в [Task2](./Task2/README.md#запуск-и-проверка).
