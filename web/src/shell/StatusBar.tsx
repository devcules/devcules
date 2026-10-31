import { useEffect, useState } from 'react';
import { hostInfo, type HostInfo } from '../api';

// The bottom bar: one muted monospaced strip of the machine serve
// itself is on, in the same shape as kaithos's and the same
// mutability as the token system (no bespoke colour). A value serve
// didn't say is absent: no "unknown", no dashes; a failed fetch hides
// the whole bar instead of a stale panel. The 5s cadence matches the
// repo's own approval-poll tick (Shell already calls listApprovals
// every pollEvery ms, and page-hidden pauses straighten the two
// together).
export default function StatusBar({ username }: { username: string }) {
  const [info, setInfo] = useState<HostInfo | null>(null);

  useEffect(() => {
    let alive = true;
    function poll(): void {
      void hostInfo().then(
        (res) => {
          if (!alive) {
            return;
          }
          if (res === 'unauthorized') {
            return; // 401 lands via the app's session guard shortly
          }
          if (res === 'unreachable') {
            setInfo(null);
            return;
          }
          setInfo(res);
        },
        () => {
          if (alive) {
            setInfo(null);
          }
        },
      );
    }
    poll();
    // Fixed-window poll is the right answer here; a long chain
    // would overlap the read it comes to make.
    const beat = window.setInterval(poll, 5000);
    return () => {
      alive = false;
      window.clearInterval(beat);
    };
  }, []);

  if (info === null) {
    return null;
  }

  // M/M/G: human-compact bytes on the bar. The display says GiB —
  // Linux and macOS report GiB via vm_stat / /proc/meminfo, and the
  // kernel counts pages, not SI bytes.
  const gib = (b: number): string => `${(b / 1024 ** 3).toFixed(1)}G`;
  const pct = (n: number): string => `${Math.round(n)}%`;
  const repoBase = info.repo ? basename(info.repo) : '';

  return (
    <footer
      aria-label="Machine status"
      className="mt-auto w-full border-t border-muted bg-surface px-4 py-1.5 text-tab text-muted md:pb-[env(safe-area-inset-bottom)]"
    >
      <div className="flex flex-wrap items-center justify-between gap-x-5 gap-y-1">
        <div className="flex min-w-0 items-center gap-x-4">
          <Item l="USER" accent>
            {username}
          </Item>
          <Item l="MACHINE">{info.hostname}</Item>
          {repoBase !== '' && (
            <Item l="REPO" accent>
              {repoBase}
              {info.branch !== undefined && info.branch !== '' && (
                <span className="text-accent">@{info.branch}</span>
              )}
            </Item>
          )}
        </div>
        <div className="flex items-center gap-x-4">
          <Item l="CPU">
            {pct(info.cpuPercent)}
            <span className="text-muted">·{info.cores}C</span>
          </Item>
          <Item l="MEM">
            {gib(info.memUsedBytes)}/{gib(info.memTotalBytes)} {pct(info.memPercent)}
          </Item>
          {info.gpuName !== undefined && info.gpuName !== '' && <Item l="GPU">{info.gpuName}</Item>}
        </div>
      </div>
    </footer>
  );
}

// One labelled metric. The label reads upper-case in the same tab
// size as the phone nav so the two rows read as one chrome; the
// value wears text — accent when it's the "identity" of the bar
// (user, repo) — theme colour wears too much to go on the level,
// so identity rows alone carry it.
function Item({
  l,
  accent = false,
  children,
}: {
  l: string;
  accent?: boolean;
  children: React.ReactNode;
}) {
  return (
    <span className="flex min-w-0 items-baseline gap-1">
      <span className="text-muted">{l}</span>
      <span className={`min-w-0 truncate ${accent ? 'text-accent' : 'text-text'}`}>{children}</span>
    </span>
  );
}

function basename(dir: string): string {
  const s = dir.replace(/\/+$/, '');
  const i = s.lastIndexOf('/');
  return i < 0 ? s : s.slice(i + 1);
}
