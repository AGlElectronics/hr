import { useState } from 'react';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';

import { listPeople, PERSON_STATUSES } from '@/api/people';
import { Pencil } from 'lucide-react';

import { PersonQuickPanel } from '@/components/PersonQuickPanel';
import { PersonStatusBadge } from '@/components/PersonStatusBadge';

export function PeoplePage() {
  const [q, setQ] = useState('');
  const [status, setStatus] = useState('');
  const [open, setOpen] = useState<string | null>(null);

  const peopleQuery = useQuery({
    queryKey: ['people', q, status],
    queryFn: () => listPeople({ q: q || undefined, status: status || undefined }),
    placeholderData: keepPreviousData,
  });

  const items = peopleQuery.data?.items ?? [];
  const missing = items.filter((p) => p.email_missing).length;

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
          className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground">
          Onboard a volunteer
        </Link>
      </div>

      {missing > 0 && (
        <p className="rounded-md border border-warning/40 bg-warning-soft px-4 py-2 text-sm text-warning-foreground">
          {missing} {missing === 1 ? 'person has' : 'people have'} no email yet. Email is required for login and
          provisioning — open them and add one.
        </p>
      )}

      <section className="rounded-lg border border-border bg-card">
        <header className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
          <span className="font-semibold">All people</span>
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search name or email…"
            className="ml-auto w-56 rounded-md border border-input bg-background px-3 py-1.5 text-sm" />
          <select value={status} onChange={(e) => setStatus(e.target.value)}
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
              <th className="px-4 py-2 hidden md:table-cell">Phone</th>
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
                  {p.email_missing ? (
                    <span className="text-xs font-semibold text-warning">Email missing</span>
                  ) : (
                    <>
                      <div>{p.email}</div>
                      {p.org_email && <div className="text-xs text-muted-foreground">{p.org_email}</div>}
                    </>
                  )}
                </td>
                <td className="px-4 py-2 hidden md:table-cell text-muted-foreground">{p.phone || '—'}</td>
                <td className="px-4 py-2"><PersonStatusBadge status={p.status} /></td>
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
      </section>
      {open && <PersonQuickPanel personId={open} onClose={() => setOpen(null)} />}
    </div>
  );
}
