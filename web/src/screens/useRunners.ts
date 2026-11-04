import { useCallback, useEffect, useState } from 'react';
import { getApprovalSettings, type AccessTarget } from '../api';

// useRunners lists the runners linked to this Devcules, for the screens
// that used to call the terminal API with no target. With one runner
// that is the right (and only) choice; with two — a second machine's
// runner, or a stray `devcules runner` — an untargeted call is a 400,
// which used to surface as "Couldn't reach the server" on a screen
// where nothing was unreachable.
//
// The list comes from /api/approvals/settings because that already
// names every linked runner; the terminal API has no "list runners"
// route of its own.
export type RunnersState =
  | { state: 'loading' }
  | { state: 'unreachable' }
  | { state: 'ready'; runners: string[]; host: string };

export function useRunners(onSessionExpired?: () => void): RunnersState {
  const [state, setState] = useState<RunnersState>({ state: 'loading' });

  const load = useCallback(() => {
    void getApprovalSettings().then((res) => {
      if (res === 'unauthorized') {
        onSessionExpired?.();
        return;
      }
      if (res === 'unreachable' || !Array.isArray(res.targets)) {
        setState({ state: 'unreachable' });
        return;
      }
      // Only runners that are actually linked right now can host a
      // terminal; a remembered-but-absent target must not be offered.
      // The host computer leads the list: serve runs beside it, so it is
      // the natural default and a phone lands on the Mac it already
      // knows rather than on whichever name happens to sort first. The
      // rest stay alphabetical, so the order is otherwise stable.
      const live = (res.targets as AccessTarget[])
        .filter((t) => t.linked && t.runner !== '')
        .map((t) => t.runner)
        .sort();
      const host = (res.targets as AccessTarget[]).find((t) => t.linked && t.host === true)?.runner ?? '';
      const ordered = live.includes(host) ? [host, ...live.filter((n) => n !== host)] : live;
      setState({ state: 'ready', runners: ordered, host: live.includes(host) ? host : '' });
    });
  }, [onSessionExpired]);

  useEffect(() => {
    load();
  }, [load]);

  return state;
}
