// Клиент BFF: браузер хранит только HttpOnly cookie сессии, токенов во фронтенде нет.

export interface User {
  sub: string;
  username: string;
  name: string;
  roles: string[];
}

export interface Day {
  date: string;
  movements: number;
  avg_reaction_ms: number;
  p95_reaction_ms: number;
  max_reaction_ms: number;
  slow_reactions: number;
  errors: number;
  avg_battery_pct: number;
  min_battery_pct: number;
}

export interface ProsthesisReport {
  serial: string;
  model: string;
  days: Day[];
}

export interface Report {
  user_id: string;
  full_name?: string;
  country?: string;
  period: { from: string; to: string };
  processed_until: string;
  truncated: boolean;
  prostheses: ProsthesisReport[];
}

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public details?: Record<string, unknown>,
  ) {
    super(message);
  }
}

const BFF = '/bff';

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(`${BFF}${path}`, {
    ...init,
    credentials: 'same-origin',
    headers: { 'X-Requested-With': 'bionicpro-web', ...(init.headers || {}) },
  });
  if (!response.ok) {
    let code = 'http_error';
    let message = `HTTP ${response.status}`;
    let details: Record<string, unknown> | undefined;
    try {
      const body = await response.json();
      code = body.error?.code ?? code;
      message = body.error?.message ?? message;
      details = body.error?.details;
    } catch {
      // тело не JSON — оставляем общее сообщение
    }
    throw new ApiError(response.status, code, message, details);
  }
  return response.status === 204 ? (undefined as T) : response.json();
}

export const loginUrl = `${BFF}/auth/login`;

export async function fetchMe(): Promise<User | null> {
  try {
    return await request<User>('/auth/me');
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) {
      return null;
    }
    throw e;
  }
}

export function fetchReport(from: string, to: string): Promise<Report> {
  const params = new URLSearchParams();
  if (from) params.set('from', from);
  if (to) params.set('to', to);
  const query = params.toString();
  return request<Report>(`/api/reports${query ? `?${query}` : ''}`);
}

export function logout(): Promise<void> {
  return request<void>('/auth/logout', { method: 'POST' });
}
