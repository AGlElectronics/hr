import { useState } from 'react';
import { keepPreviousData, useQuery } from '@tanstack/react-query';

import { listAudit, type AuditEvent } from '@/api/admin';
import { PAGE_SIZE } from '@/api/people';
import { SearchInput } from '@/components/SearchInput';

const ENTITY_TYPES = [
  'person',
  'employment_draft',
  'department',
  'access_bundle',
  'role_requirement',
  'review_item',
  'import_batch',
  'import_template',
  'settings',
  'den_link',
];

/** Who did what in HR. Access history of offboarded people is kept until its purge date. */
export function AuditPage() {
  const [actor, setActor] = useState('');
  const [action, setAction] = useState('');
  const [entityType, setEntityType] = useState('');
  const [offset, setOffset] = useState(0);
  const [open, setOpen] = useState<string | null>(null);
  const query = useQuery({
    queryKey: ['audit', actor, action, entityType, offset],
    queryFn: () =>
      listAudit({ actor: actor || undefined, action: action || undefined, entity_type: entityType || undefined, limit: PAGE_SIZE, offset }),
    placeholderData: keepPreviousData,
  });
  const items = query.data?.items ?? [];
  const total = query.data?.total ?? 0;
  const reset = <T,>(set: (v: T) => void) => (v: T) => { set(v); setOffset(0); };

  return (
    <div className="p-6 space-y-6">
      <div>
        <h1 className="text-2xl font-bold">Audit log</h1>
        <p className="text-sm text-muted-foreground">
          Every change made in HR, newest first. The access history of offboarded people is kept for the retention period, then deleted.
        </p>
      </div>
      <section className="rounded-lg border border-border bg-card">
        <header className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
          <SearchInput value={actor} onChange={reset(setActor)} placeholder="Who (name or subject)…" className="w-56" />
          <SearchInput value={action} onChange={reset(setAction)} placeholder="Action, e.g. person.update" className="w-56" />
          <select value={entityType} onChange={(e) => reset(setEntityType)(e.target.value)} aria-label="Type"
            className="rounded-md border border-input bg-background px-3 py-1.5 text-sm">
            <option value="">Everything</option>
            {ENTITY_TYPES.map((t) => <option key={t} value={t}>{t}</option>)}
          </select>
        </header>
        {query.isLoading && <p className="px-4 py-3 text-muted-foreground">Loading…</p>}
        {query.error && <p className="px-4 py-3 text-destructive">{(query.error as Error).message}</p>}
        <table className="w-full text-sm">
          <thead className="text-left text-xs uppercase text-muted-foreground">
            <tr className="border-b border-border">
              <th className="px-4 py-2">When</th>
              <th className="px-4 py-2">Who</th>
              <th className="px-4 py-2">Action</th>
              <th className="px-4 py-2 hidden md:table-cell">On</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {items.map((e) => (
              <AuditRow key={e.id} event={e} open={open === e.id} onToggle={() => setOpen(open === e.id ? null : e.id)} />
            ))}
            {!query.isLoading && items.length === 0 && (
              <tr><td colSpan={4} className="px-4 py-6 text-muted-foreground">No entries match.</td></tr>
            )}
          </tbody>
        </table>
        {total > PAGE_SIZE && (
          <footer className="flex items-center justify-between border-t border-border px-4 py-2 text-sm">
            <span className="text-muted-foreground">{offset + 1}–{Math.min(offset + PAGE_SIZE, total)} of {total}</span>
            <span className="flex gap-2">
              <button type="button" disabled={offset === 0} onClick={() => setOffset((o) => Math.max(0, o - PAGE_SIZE))}
                className="rounded-pill border border-border px-3 py-1 disabled:opacity-50">Previous</button>
              <button type="button" disabled={offset + PAGE_SIZE >= total} onClick={() => setOffset((o) => o + PAGE_SIZE)}
                className="rounded-pill border border-border px-3 py-1 disabled:opacity-50">Next</button>
            </span>
          </footer>
        )}
      </section>
    </div>
  );
}

function AuditRow({ event: e, open, onToggle }: { event: AuditEvent; open: boolean; onToggle: () => void }) {
  const hasDetails = e.metadata && Object.keys(e.metadata).length > 0;
  return (
    <>
      <tr onClick={hasDetails ? onToggle : undefined} className={hasDetails ? 'cursor-pointer hover:bg-muted/40' : ''}>
        <td className="px-4 py-2 whitespace-nowrap text-muted-foreground">{new Date(e.occurred_at).toLocaleString()}</td>
        <td className="px-4 py-2" title={e.actor_subject ? `${e.actor_issuer} · ${e.actor_subject}` : undefined}>{e.actor_name || '—'}</td>
        <td className="px-4 py-2 font-mono text-xs">
          {e.action}
          {e.purge_after && (
            <span className="ml-2 rounded-pill bg-muted px-1.5 py-0.5 font-sans text-[11px] text-muted-foreground">
              kept until {new Date(e.purge_after).toLocaleDateString()}
            </span>
          )}
        </td>
        <td className="px-4 py-2 hidden md:table-cell text-xs text-muted-foreground">
          {e.entity_type} <span className="font-mono">{e.entity_id.slice(0, 8)}</span>
        </td>
      </tr>
      {open && hasDetails && (
        <tr>
          <td colSpan={4} className="bg-muted/30 px-4 py-2">
            <pre className="overflow-x-auto whitespace-pre-wrap text-xs">{JSON.stringify(e.metadata, null, 2)}</pre>
          </td>
        </tr>
      )}
    </>
  );
}
