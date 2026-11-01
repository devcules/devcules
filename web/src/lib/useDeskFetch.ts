import { useCallback, useEffect, useRef, useState } from 'react';
import { deskError, type Result } from './deskApi';

export type DeskData = Record<string, unknown> & { updated_at?: string; stale?: boolean; configured?: boolean };

// useDeskFetch — one widget's data. A bump of `syncKey` (the Sync button)
// refetches with refresh, which bypasses the server cache.
export function useDeskFetch(
  fetcher: (refresh: boolean) => Promise<Result<DeskData>>,
  syncKey: number,
  onUnauthorized?: () => void,
) {
  const [data, setData] = useState<DeskData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const lastSync = useRef(syncKey);

  const load = useCallback(
    async (refresh: boolean) => {
      setLoading(true);
      const res = await fetcher(refresh);
      if (res === 'unauthorized') {
        onUnauthorized?.();
        setError('session expired');
        setLoading(false);
        return;
      }
      if (res === 'unreachable') {
        setError('server unreachable');
        setLoading(false);
        return;
      }
      const dErr = deskError(res);
      if (dErr) {
        setError(dErr);
        setLoading(false);
        return;
      }
      setError('');
      setData(res as DeskData);
      setLoading(false);
    },
    [fetcher, onUnauthorized],
  );

  useEffect(() => {
    const force = lastSync.current !== syncKey;
    lastSync.current = syncKey;
    void load(force);
  }, [load, syncKey]);

  return { data, loading, error, reload: () => load(true) };
}

export function fmtTime(ts?: string): string {
  if (!ts) return '';
  const d = new Date(ts);
  if (isNaN(d.getTime())) return '';
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}
