import React, { useState } from 'react';
import { ApiError, fetchReport, logout, Report, User } from '../api';

interface Props {
  user: User;
  onLoggedOut: () => void;
}

const fmt = (n: number, digits = 0) => n.toLocaleString('ru-RU', { maximumFractionDigits: digits });

function errorText(e: unknown): string {
  if (!(e instanceof ApiError)) {
    return e instanceof Error ? e.message : 'Ошибка при получении отчёта';
  }
  switch (e.code) {
    case 'period_not_processed':
      return `Данные за этот период ещё не обработаны. Доступны данные до ${new Date(
        String(e.details?.processed_until),
      ).toLocaleString('ru-RU')}.`;
    case 'report_not_ready':
      return 'Данные для отчётов ещё готовятся. Попробуйте позже.';
    case 'forbidden':
      return 'Отчёты доступны только пилотам протезов и только по собственным протезам.';
    case 'invalid_period':
    case 'invalid_request':
      return `Некорректный период: ${e.message}`;
    default:
      return e.message;
  }
}

const ReportPage: React.FC<Props> = ({ user, onLoggedOut }) => {
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [report, setReport] = useState<Report | null>(null);

  const getReport = async () => {
    setLoading(true);
    setError(null);
    try {
      setReport(await fetchReport(from, to));
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        onLoggedOut();
        return;
      }
      setReport(null);
      setError(errorText(e));
    } finally {
      setLoading(false);
    }
  };

  const doLogout = async () => {
    try {
      await logout();
    } finally {
      onLoggedOut();
    }
  };

  return (
    <div className="min-h-screen bg-gray-100 p-6">
      <div className="max-w-5xl mx-auto bg-white rounded-lg shadow-md p-8">
        <div className="flex justify-between items-start mb-6">
          <div>
            <h1 className="text-2xl font-bold">Отчёт о работе протеза</h1>
            <p className="text-gray-600">{user.name || user.username}</p>
          </div>
          <button onClick={doLogout} className="px-3 py-1 border rounded hover:bg-gray-50">
            Выйти
          </button>
        </div>

        <div className="flex flex-wrap items-end gap-4 mb-6">
          <label className="flex flex-col text-sm">
            С
            <input type="date" value={from} onChange={(e) => setFrom(e.target.value)} className="border rounded px-2 py-1" />
          </label>
          <label className="flex flex-col text-sm">
            По
            <input type="date" value={to} onChange={(e) => setTo(e.target.value)} className="border rounded px-2 py-1" />
          </label>
          <button
            onClick={getReport}
            disabled={loading}
            className={`px-4 py-2 bg-blue-500 text-white rounded hover:bg-blue-600 ${loading ? 'opacity-50 cursor-not-allowed' : ''}`}
          >
            {loading ? 'Формируем отчёт…' : 'Получить отчёт'}
          </button>
        </div>
        <p className="text-xs text-gray-500 mb-4">Без дат — последние 30 дней, по которым данные уже обработаны.</p>

        {error && <div className="mb-4 p-4 bg-red-100 text-red-700 rounded">{error}</div>}

        {report && (
          <div>
            <p className="text-sm text-gray-600 mb-4">
              Период: {report.period.from} — {report.period.to}. Данные обработаны до{' '}
              {new Date(report.processed_until).toLocaleString('ru-RU')}.
              {report.truncated && ' Запрошенный период обрезан: более поздние данные ещё не обработаны.'}
            </p>
            {report.prostheses.length === 0 && (
              <div className="p-4 bg-yellow-50 text-yellow-800 rounded">
                За выбранный период данных нет (возможно, сбор телеметрии отключён).
              </div>
            )}
            {report.prostheses.map((p) => (
              <div key={p.serial} className="mb-8">
                <h2 className="text-xl font-semibold mb-2">
                  {p.model} <span className="text-gray-500 text-base">({p.serial})</span>
                </h2>
                <div className="overflow-x-auto">
                  <table className="min-w-full text-sm">
                    <thead>
                      <tr className="text-left border-b">
                        <th className="py-1 pr-4">Дата</th>
                        <th className="py-1 pr-4">Движений</th>
                        <th className="py-1 pr-4">Реакция ср., мс</th>
                        <th className="py-1 pr-4">p95, мс</th>
                        <th className="py-1 pr-4">Макс., мс</th>
                        <th className="py-1 pr-4">Ошибок</th>
                        <th className="py-1 pr-4">Заряд ср., %</th>
                      </tr>
                    </thead>
                    <tbody>
                      {p.days.map((d) => (
                        <tr key={d.date} className="border-b">
                          <td className="py-1 pr-4">{d.date}</td>
                          <td className="py-1 pr-4">{fmt(d.movements)}</td>
                          <td className="py-1 pr-4">{fmt(d.avg_reaction_ms, 1)}</td>
                          <td className="py-1 pr-4">{fmt(d.p95_reaction_ms, 1)}</td>
                          <td className="py-1 pr-4">{fmt(d.max_reaction_ms)}</td>
                          <td className="py-1 pr-4">{fmt(d.errors)}</td>
                          <td className="py-1 pr-4">{fmt(d.avg_battery_pct, 1)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
};

export default ReportPage;
