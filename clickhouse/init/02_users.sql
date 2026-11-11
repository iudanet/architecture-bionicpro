-- ETL пишет во все таблицы bionicpro, сервис отчётов только читает витрину и watermark.
CREATE USER IF NOT EXISTS etl_writer IDENTIFIED WITH sha256_password BY 'etl_writer_pw';
GRANT SELECT, INSERT ON bionicpro.* TO etl_writer;

CREATE USER IF NOT EXISTS reports_reader IDENTIFIED WITH sha256_password BY 'reports_reader_pw'
    SETTINGS readonly = 1;
GRANT SELECT ON bionicpro.report_user_daily TO reports_reader;
GRANT SELECT ON bionicpro.etl_watermark TO reports_reader;
