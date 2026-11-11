import { useState } from 'react';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Link, useNavigate, useSearch } from '@tanstack/react-router';
import { Pencil } from 'lucide-react';

import { listPeople, PAGE_SIZE, PERSON_STATUSES, type PersonStatus } from '@/api/people';
import { PersonQuickPanel } from '@/components/PersonQuickPanel';
import { PersonStatusBadge } from '@/components/PersonStatusBadge';
import { SearchInput } from '@/components/SearchInput';

export function PeoplePage() {
  const { status = '' } = useSearch({ from: '/app/people' });
  const navigate = useNavigate();
  const setStatus = (s: string) =>
    void navigate({ to: '/people', search: s ? { status: s as PersonStatus } : {}, replace: true });
  const [q, setQ] = useState('');
  const [offset, setOffset] = useState(0);
  const [open, setOpen] = useState<string | null>(null);

  const peopleQuery = useQuery({
    queryKey: ['people', q, status, offset],
    queryFn: () => listPeople({ q: q || undefined, status: status || undefined, limit: PAGE_SIZE, offset }),
    placeholderData: keepPreviousData,
  });
  const items = peopleQuery.data?.items ?? [];
  const total = peopleQuery.data?.total ?? 0;

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold">People</h1>
          <p className="text-sm text-muted-foreground">
            Click someone for quick actions: change access, pause, offboard.
          </p>
        </div>
        <Link to="/onboarding/new"
          className="shrink-0 rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground">
          Onboard a volunteer
        </Link>
      </div>

      <section className="rounded-lg border border-border bg-card">
        <header className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
          <span className="font-semibold">{PERSON_STATUSES.find((s) => s.value === status)?.label ?? 'All'} people</span>
          <SearchInput value={q} onChange={(v) => { setQ(v); setOffset(0); }} placeholder="Name, nickname or email…" className="ml-auto w-64" />
          <select value={status} onChange={(e) => { setOffset(0); setStatus(e.target.value); }} aria-label="Status"
            className="rounded-md border border-input bg-background px-3 py-1.5 text-sm">
            <option value="">All statuses</option>
            {PERSON_STATUSES.map((s) => (
              <option key={s.value} value={s.value}>{s.label}</option>
            ))}
          </select>
        </header>
        {peopleQuery.isLoading && <p className="px-4 py-3 text-muted-foreground">Loading…</p>}
        <table className="w-full text-sm">
          <thead className="text-left text-xs uppercase text-muted-foreground">
            <tr className="border-b border-border">
              <th className="px-4 py-2">Name</th>
              <th className="px-4 py-2">Email</th>
              <th className="px-4 py-2 hidden md:table-cell">Login name</th>
              <th className="px-4 py-2">Status</th>
              <th className="px-4 py-2" />
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {items.map((p) => (
              <tr key={p.id} onClick={() => setOpen(p.id)}
                className={`cursor-pointer hover:bg-muted/40 ${open === p.id ? 'bg-primary-soft/50' : ''}`}>
                <td className="px-4 py-2">
                  <span className="font-medium">{p.display_name}</span>
                  {p.display_name !== p.legal_name && (
                    <div className="text-xs text-muted-foreground">{p.legal_name}</div>
                  )}
                </td>
                <td className="px-4 py-2">
                  <div>{p.email}</div>
                  {p.org_email && <div className="text-xs text-muted-foreground">{p.org_email}</div>}
                </td>
                <td className="px-4 py-2 hidden md:table-cell text-muted-foreground">{p.username || '—'}</td>
                <td className="px-4 py-2">
                  <PersonStatusBadge status={p.status} />
                  {p.is_banned && <span className="ml-1 rounded-pill bg-destructive/10 px-1.5 py-0.5 text-[11px] font-semibold text-destructive">Banned</span>}
                </td>
                <td className="px-4 py-2 text-right whitespace-nowrap" onClick={(e) => e.stopPropagation()}>
                  <Link to="/people/$id" params={{ id: p.id }} search={{ edit: true }}
                    className="inline-flex items-center gap-1 text-xs text-primary hover:underline">
                    <Pencil className="h-3 w-3" /> Edit
                  </Link>
                </td>
              </tr>
            ))}
            {!peopleQuery.isLoading && items.length === 0 && (
              <tr>
                <td colSpan={5} className="px-4 py-6 text-sm text-muted-foreground">
                  {q || status ? 'No one matches this filter.' : 'No people yet.'}
                </td>
              </tr>
            )}
          </tbody>
        </table>
        {total > PAGE_SIZE && (
          <footer className="flex items-center justify-between border-t border-border px-4 py-2 text-sm">
            <span className="text-muted-foreground">
              {offset + 1}–{Math.min(offset + PAGE_SIZE, total)} of {total}
            </span>
            <span className="flex gap-2">
              <button type="button" disabled={offset === 0} onClick={() => setOffset((o) => Math.max(0, o - PAGE_SIZE))}
                className="rounded-pill border border-border px-3 py-1 disabled:opacity-50">Previous</button>
              <button type="button" disabled={offset + PAGE_SIZE >= total} onClick={() => setOffset((o) => o + PAGE_SIZE)}
                className="rounded-pill border border-border px-3 py-1 disabled:opacity-50">Next</button>
            </span>
          </footer>
        )}
      </section>
      {open && <PersonQuickPanel personId={open} onClose={() => setOpen(null)} />}
    </div>
  );
}
