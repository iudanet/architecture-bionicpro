#!/bin/sh
set -eu
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d crm <<'SQL'
CREATE TABLE clients (
    id          BIGSERIAL PRIMARY KEY,
    sso_user_id UUID        NOT NULL UNIQUE, -- sub пользователя в Keycloak
    full_name   TEXT        NOT NULL,
    email       TEXT        NOT NULL,
    phone       TEXT,
    address     TEXT,
    country     CHAR(2)     NOT NULL DEFAULT 'RU',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE prostheses (
    serial            TEXT PRIMARY KEY,
    client_id         BIGINT      NOT NULL REFERENCES clients (id),
    model             TEXT        NOT NULL,
    issued_at         DATE        NOT NULL,
    telemetry_enabled BOOLEAN     NOT NULL DEFAULT true, -- пользователь может отключить сбор данных
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO clients (sso_user_id, full_name, email, phone, address, country) VALUES
    ('7c1e4a52-3b8d-4f0e-9a61-2d5f8c9e0a11', 'Иван Петров',    'prothetic1@example.com', '+7 900 000-00-01', 'Москва',          'RU'),
    ('a93f0d27-6e4b-4c18-b2d5-8e1f7a3c5b22', 'Мария Соколова', 'prothetic2@example.com', '+7 900 000-00-02', 'Санкт-Петербург', 'RU'),
    ('e5b28c94-1a7f-4d36-8c0e-4b9d2f6a7c33', 'Олег Смирнов',   'prothetic3@example.com', '+7 900 000-00-03', 'Казань',          'RU');

INSERT INTO prostheses (serial, client_id, model, issued_at, telemetry_enabled) VALUES
    ('BP-ARM-0001', 1, 'BionicPRO Arm X2',  '2026-03-12', true),
    ('BP-ARM-0002', 2, 'BionicPRO Arm X2',  '2026-05-20', true),
    ('BP-LEG-0003', 2, 'BionicPRO Leg K1',  '2026-08-02', true),
    ('BP-ARM-0004', 3, 'BionicPRO Hand S1', '2026-09-15', false);

GRANT CONNECT ON DATABASE crm TO crm_reader;
GRANT USAGE ON SCHEMA public TO crm_reader;
GRANT SELECT ON clients, prostheses TO crm_reader;
SQL
