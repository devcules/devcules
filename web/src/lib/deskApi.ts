// Desk API: typed wrappers over /api/desk/* and /api/secrets. Mirrors the
// established Devcules pattern (unauthorized | unreachable | data), so the
// tiles can read session expiry and offline the same way the rest of the
// app does.

export type Result<T> = T | 'unauthorized' | 'unreachable';

async function req<T>(path: string, init?: RequestInit, isEmpty = false): Promise<Result<T>> {
  let res: Response;
  try {
    res = await fetch(path, init);
  } catch {
    return 'unreachable';
  }
  if (res.status === 401) return 'unauthorized';
  if (res.status >= 500) return 'unreachable';
  if (isEmpty) return (res.status < 300 ? (true as unknown as T) : 'unreachable') as Result<T>;
  if (!res.ok) {
    // A 400/502 from a desk proxy carries a readable line ("no Tavily
    // key…") the tile can show instead of a generic offline.
    try {
      const j = (await res.json()) as { error?: string };
      return ({ __deskError: j.error ?? `HTTP ${res.status}` }) as unknown as Result<T>;
    } catch {
      return 'unreachable';
    }
  }
  try {
    return (await res.json()) as T;
  } catch {
    return 'unreachable';
  }
}

export function deskError<T>(r: Result<T>): string | null {
  if (typeof r === 'object' && r !== null && '__deskError' in (r as object)) {
    return String((r as unknown as { __deskError: unknown }).__deskError);
  }
  return null;
}

function put(path: string, body: unknown) {
  return req<true>(path, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }, true);
}

export type Prefs = { version: number; widgets: unknown[]; layouts: Record<string, unknown>; seen?: Record<string, string> };
export const getPrefs = () => req<Prefs>('/api/desk/prefs');
export const putPrefs = (p: Prefs) => put('/api/desk/prefs', p);

export type Sources = Record<string, unknown> & {
  location?: string; news_security_query?: string; news_tech_query?: string; news_custom_query?: string;
  has_tavily?: boolean; has_ticket_token?: boolean;
  ticket_provider?: string; ticket_site?: string; ticket_email?: string; ticket_jql?: string; ticket_project?: string;
  ics_feeds?: Array<{ label: string; url: string }>;
  tavily_key?: string; ticket_token?: string;
};
export const getSources = () => req<Sources>('/api/desk/sources');
export const putSources = (s: Sources) => put('/api/desk/sources', s);

export const getWeather = (refresh = false) => req<Record<string, unknown>>(`/api/desk/weather${refresh ? '?refresh=1' : ''}`);
export const getNews = (which: string, refresh = false) => req<{ configured?: boolean; items?: unknown[]; stale?: boolean }>(`/api/desk/news/${which}${refresh ? '?refresh=1' : ''}`);
export const getTickets = (refresh = false) => req<Record<string, unknown>>(`/api/desk/tickets${refresh ? '?refresh=1' : ''}`);
export const getMeetings = (refresh = false) => req<Record<string, unknown>>(`/api/desk/meetings${refresh ? '?refresh=1' : ''}`);

export const listSecrets = () => req<{ names: string[] }>('/api/secrets');
export const putSecret = (name: string, value: string) => put('/api/secrets', { name, value });
export const deleteSecret = async (name: string): Promise<Result<true>> => {
  let res: Response;
  try {
    res = await fetch(`/api/secrets/${encodeURIComponent(name)}`, { method: 'DELETE' });
  } catch {
    return 'unreachable';
  }
  if (res.status === 401) return 'unauthorized';
  return res.status === 204 ? true : 'unreachable';
};
