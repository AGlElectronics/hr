import { useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { ExternalLink, Pencil, X } from 'lucide-react';

import { getPerson } from '@/api/people';
import { PersonActions } from '@/components/PersonActions';
import { PersonStatusBadge } from '@/components/PersonStatusBadge';

/** Slide-over with a person's summary and every quick action, so HR never has to leave the list. */
export function PersonQuickPanel({ personId, onClose }: { personId: string; onClose: () => void }) {
  const person = useQuery({ queryKey: ['person', personId], queryFn: () => getPerson(personId) });

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const p = person.data;
  return (
    <div className="fixed inset-0 z-50 flex justify-end" role="dialog" aria-modal="true">
      <button type="button" aria-label="Close" className="absolute inset-0 bg-black/30" onClick={onClose} />
      <aside className="relative flex h-full w-full max-w-md flex-col overflow-y-auto border-l border-border bg-card shadow-xl">
        <header className="flex items-start justify-between gap-3 border-b border-border px-5 py-4">
          <div className="min-w-0">
            <h2 className="truncate text-lg font-bold">{p?.display_name ?? 'Loading…'}</h2>
            {p && (
              <p className="text-sm text-muted-foreground">
                {p.legal_name !== p.display_name && <>{p.legal_name} · </>}
                <PersonStatusBadge status={p.status} />
              </p>
            )}
          </div>
          <button type="button" onClick={onClose} className="rounded-pill p-1 hover:bg-muted" aria-label="Close panel">
            <X className="h-5 w-5" />
          </button>
        </header>

        {p && (
          <div className="space-y-5 px-5 py-4">
            <div className="flex flex-wrap gap-2">
              <Link to="/people/$id" params={{ id: p.id }} search={{ edit: true }}
                className="inline-flex items-center gap-1.5 rounded-pill bg-primary px-4 py-1.5 text-sm font-semibold text-primary-foreground">
                <Pencil className="h-3.5 w-3.5" /> Edit profile
              </Link>
              <Link to="/people/$id" params={{ id: p.id }}
                className="inline-flex items-center gap-1.5 rounded-pill border border-border px-4 py-1.5 text-sm">
                <ExternalLink className="h-3.5 w-3.5" /> Full profile
              </Link>
            </div>

            <dl className="grid grid-cols-[7rem_1fr] gap-x-2 gap-y-1 text-sm">
              <dt className="text-muted-foreground">Email</dt>
              <dd className="break-words">
                {p.email_missing ? <span className="font-semibold text-amber-700">Missing</span> : p.email}
              </dd>
              <dt className="text-muted-foreground">Org email</dt>
              <dd className="break-words">{p.org_email || '—'}</dd>
              <dt className="text-muted-foreground">Phone</dt>
              <dd>{p.phone || '—'}</dd>
              {p.is_banned && (
                <>
                  <dt className="text-muted-foreground">Flag</dt>
                  <dd className="font-semibold text-destructive">Banned</dd>
                </>
              )}
            </dl>

            <PersonActions personId={p.id} />
          </div>
        )}
        {person.error && <p className="p-5 text-sm text-destructive">{(person.error as Error).message}</p>}
      </aside>
    </div>
  );
}
