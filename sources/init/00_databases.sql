CREATE DATABASE crm;
CREATE DATABASE telemetry;

CREATE ROLE crm_reader LOGIN PASSWORD 'crm_reader_pw';
CREATE ROLE telemetry_reader LOGIN PASSWORD 'telemetry_reader_pw';
CREATE ROLE telemetry_writer LOGIN PASSWORD 'telemetry_writer_pw';
