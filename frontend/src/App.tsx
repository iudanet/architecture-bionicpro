import React, { useCallback, useEffect, useState } from 'react';
import ReportPage from './components/ReportPage';
import { fetchMe, loginUrl, User } from './api';

const App: React.FC = () => {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const loginError = new URLSearchParams(window.location.search).get('login_error');

  const loadUser = useCallback(async () => {
    setLoading(true);
    try {
      setUser(await fetchMe());
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Не удалось связаться с сервером');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadUser();
  }, [loadUser]);

  if (loading) {
    return <div className="flex items-center justify-center min-h-screen bg-gray-100">Загрузка…</div>;
  }

  if (!user) {
    return (
      <div className="flex flex-col items-center justify-center min-h-screen bg-gray-100 gap-4">
        <h1 className="text-2xl font-bold">BionicPRO</h1>
        <p className="text-gray-600">Отчёты о работе протеза</p>
        <a href={loginUrl} className="px-4 py-2 bg-blue-500 text-white rounded hover:bg-blue-600">
          Войти
        </a>
        {(loginError || error) && (
          <div className="p-4 bg-red-100 text-red-700 rounded">
            {error ?? 'Не удалось выполнить вход, попробуйте ещё раз'}
          </div>
        )}
      </div>
    );
  }

  return <ReportPage user={user} onLoggedOut={() => setUser(null)} />;
};

export default App;
