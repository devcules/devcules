// DeskSources — the drawer behind ⚙: where you are, the calendar feeds,
// the news searches, the tickets connection, and the Tavily key. Tokens go
// in write-only (blank keeps the stored one); GET answers presence flags.

import { useEffect, useState } from 'react';
import { getSources, putSources, type Sources } from '../../lib/deskApi';
import { toast } from '../../lib/toast';

export default function DeskSources({ onClose, onSaved, onUnauthorized }: { onClose: () => void; onSaved: () => void; onUnauthorized?: () => void }) {
  const [cur, setCur] = useState<Sources | null>(null);
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState('');
  const [f, setF] = useState<Sources>({ ics_feeds: [{ label: 'Calendar', url: '' }] });
  const [clearTavily, setClearTavily] = useState(false);
  const [clearTicket, setClearTicket] = useState(false);

  useEffect(() => {
    void getSources().then((r) => {
      if (r === 'unauthorized') { onUnauthorized?.(); return; }
      if (r === 'unreachable') { setErr('server unreachable'); setCur({}); return; }
      const s = r as Sources;
      setCur(s);
      setF({ ...s, ics_feeds: Array.isArray(s.ics_feeds) && s.ics_feeds.length ? s.ics_feeds : [{ label: 'Calendar', url: '' }], tavily_key: '', ticket_token: '' });
    });
  }, [onUnauthorized]);

  const set = (k: keyof Sources) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setF((p) => ({ ...p, [k]: e.target.value }));
  const setFeed = (i: number, k: 'label' | 'url') => (e: React.ChangeEvent<HTMLInputElement>) =>
    setF((p) => ({ ...p, ics_feeds: (p.ics_feeds || []).map((fd, j) => (j === i ? { ...fd, [k]: e.target.value } : fd)) }));

  if (cur === null) return <Drawer onClose={onClose}><div className="p-4 text-body text-muted">loading…</div></Drawer>;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    const body: Sources = {
      ...f,
      ics_feeds: (f.ics_feeds || []).filter((fd) => fd.url.trim()),
      ...(clearTavily ? { tavily_clear: true } : {}),
      ...(clearTicket ? { ticket_clear: true } : {}),
    } as Sources;
    const r = await putSources(body);
    setSaving(false);
    if (r === 'unauthorized') { onUnauthorized?.(); return; }
    if (r !== true) { setErr('save failed — try again'); return; }
    toast('Sources saved');
    onSaved();
    onClose();
  };

  const provider = String(f.ticket_provider ?? 'jira');
  return (
    <Drawer onClose={onClose}>
      <form onSubmit={submit} className="flex flex-col gap-4 p-4">
        <h2 className="text-lead font-semibold text-text">Desk sources</h2>

        <label className="flex flex-col gap-1 text-body text-text">
          Where you are (weather)
          <input className="min-h-11 rounded bg-base px-3 text-body text-text" value={String(f.location ?? '')} onChange={set('location')} placeholder="Berlin, Austin, Tokyo…" />
        </label>

        <fieldset className="flex flex-col gap-2">
          <legend className="mb-1 text-body font-semibold text-text">Calendar feeds</legend>
          <p className="text-tab text-muted">Published ICS addresses — Google Calendar → Settings → Integrate → Secret address in iCal format; Outlook → Share → Publish. No sign-in needed.</p>
          {(f.ics_feeds || []).map((fd, i) => (
            <div key={i} className="flex gap-2">
              <input aria-label="feed label" className="w-28 min-h-11 rounded bg-base px-3 text-body text-text" value={fd.label} onChange={setFeed(i, 'label')} placeholder="Work" />
              <input aria-label="feed ics url" className="min-w-0 flex-1 min-h-11 rounded bg-base px-3 font-mono text-body text-text" value={fd.url} onChange={setFeed(i, 'url')} placeholder="https://…/basic.ics" />
            </div>
          ))}
          <button type="button" className="self-start rounded bg-base px-3 py-1 text-body text-text" onClick={() => setF((p) => ({ ...p, ics_feeds: [...(p.ics_feeds || []), { label: '', url: '' }] }))}>+ another feed</button>
        </fieldset>

        <fieldset className="flex flex-col gap-2">
          <legend className="mb-1 text-body font-semibold text-text">News searches</legend>
          <label className="flex flex-col gap-1 text-body text-text">Security<input className="min-h-11 rounded bg-base px-3 text-body text-text" value={String(f.news_security_query ?? '')} onChange={set('news_security_query')} placeholder="data breach CVE" /></label>
          <label className="flex flex-col gap-1 text-body text-text">Tech<input className="min-h-11 rounded bg-base px-3 text-body text-text" value={String(f.news_tech_query ?? '')} onChange={set('news_tech_query')} placeholder="AI releases" /></label>
          <label className="flex flex-col gap-1 text-body text-text">Your own feed<input className="min-h-11 rounded bg-base px-3 text-body text-text" value={String(f.news_custom_query ?? '')} onChange={set('news_custom_query')} placeholder="kubernetes" /></label>
          <label className="flex flex-col gap-1 text-body text-text">
            <span className="flex items-center justify-between gap-2">
              <span>Tavily key {cur.has_tavily && !clearTavily ? <span className="text-tab text-muted">(stored — leave blank to keep)</span> : <span className="text-tab text-muted">(optional — without it the feeds use free public search)</span>}</span>
              {cur.has_tavily && (
                <button type="button" className="text-tab text-red-400 hover:underline" onClick={() => setClearTavily((c) => !c)}>
                  {clearTavily ? 'keep it after all' : 'remove stored key'}
                </button>
              )}
            </span>
            <input type="password" className="min-h-11 rounded bg-base px-3 font-mono text-body text-text" value={String(f.tavily_key ?? '')} onChange={set('tavily_key')} placeholder="tvly-…" autoComplete="off" />
          </label>
        </fieldset>

        <fieldset className="flex flex-col gap-2">
          <legend className="mb-1 text-body font-semibold text-text">Tickets</legend>
          <label className="flex flex-col gap-1 text-body text-text">
            Tracker
            <select className="min-h-11 rounded bg-base px-3 text-body text-text" value={provider} onChange={set('ticket_provider')}>
              <option value="jira">Jira</option>
              <option value="asana">Asana</option>
              <option value="linear">Linear</option>
            </select>
          </label>
          {provider === 'jira' && (
            <>
              <label className="flex flex-col gap-1 text-body text-text">Site<input className="min-h-11 rounded bg-base px-3 text-body text-text" value={String(f.ticket_site ?? '')} onChange={set('ticket_site')} placeholder="https://you.atlassian.net" /></label>
              <label className="flex flex-col gap-1 text-body text-text">Email<input className="min-h-11 rounded bg-base px-3 text-body text-text" value={String(f.ticket_email ?? '')} onChange={set('ticket_email')} placeholder="you@work" /></label>
              <label className="flex flex-col gap-1 text-body text-text">JQL (optional)<input className="min-h-11 rounded bg-base px-3 font-mono text-tab text-text" value={String(f.ticket_jql ?? '')} onChange={set('ticket_jql')} placeholder="assignee = currentUser() AND resolution = Unresolved" /></label>
            </>
          )}
          {provider === 'asana' && (
            <label className="flex flex-col gap-1 text-body text-text">Workspace gid<input className="min-h-11 rounded bg-base px-3 text-body text-text" value={String(f.ticket_project ?? '')} onChange={set('ticket_project')} placeholder="1234567890" /></label>
          )}
          {provider === 'linear' && (
            <label className="flex flex-col gap-1 text-body text-text">Team key (optional)<input className="min-h-11 rounded bg-base px-3 text-body text-text" value={String(f.ticket_project ?? '')} onChange={set('ticket_project')} placeholder="ENG" /></label>
          )}
          <label className="flex flex-col gap-1 text-body text-text">
            <span className="flex items-center justify-between gap-2">
              <span>Token {cur.has_ticket_token && !clearTicket ? <span className="text-tab text-muted">(stored — leave blank to keep)</span> : ''}</span>
              {cur.has_ticket_token && (
                <button type="button" className="text-tab text-red-400 hover:underline" onClick={() => setClearTicket((c) => !c)}>
                  {clearTicket ? 'keep it after all' : 'remove stored token'}
                </button>
              )}
            </span>
            <input type="password" className="min-h-11 rounded bg-base px-3 font-mono text-body text-text" value={String(f.ticket_token ?? '')} onChange={set('ticket_token')} autoComplete="off" placeholder="API token" />
          </label>
          <p className="text-tab text-muted">The token never leaves the server. Blank keeps the stored one; it lives in the vault, sealed, and the browser never reads it back — manage the raw names in Secrets.</p>
        </fieldset>

        {err && <div role="alert" className="text-body text-red-400">{err}</div>}
        <div className="flex gap-2">
          <button type="submit" disabled={saving} className="min-h-11 rounded bg-accent px-4 text-body text-on-accent disabled:opacity-60">{saving ? 'saving…' : 'save sources'}</button>
          <button type="button" onClick={onClose} className="min-h-11 rounded px-4 text-body text-text">cancel</button>
        </div>
      </form>
    </Drawer>
  );
}

function Drawer({ children, onClose }: { children: React.ReactNode; onClose: () => void }) {
  return (
    <div className="fixed inset-0 z-40 flex justify-end bg-black/40" role="dialog" aria-label="Desk sources" onClick={onClose}>
      <div className="h-full w-full max-w-md overflow-y-auto border-l border-muted bg-surface" onClick={(e) => e.stopPropagation()}>
        {children}
      </div>
    </div>
  );
}
