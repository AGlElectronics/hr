import { useState } from 'react';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';

import { listDrafts, type DraftState, type EmploymentDraft } from '@/api/drafts';
import { SearchInput } from '@/components/SearchInput';
import { WaitingForTraining } from '@/components/WaitingForTraining';

const PAGE = 10;

export function OnboardingListPage() {
  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold">Onboarding</h1>
          <p className="text-sm text-muted-foreground">
            Give an approved volunteer their account in three steps: details, access, review.
          </p>
        </div>
        <Link to="/onboarding/new" className="shrink-0 rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground">
          Start onboarding
        </Link>
      </div>

      <List title="In progress" empty="No onboardings in progress." state="draft" action="Continue" />
      <List title="Being set up" empty="" state="provisioning" action="View" hideWhenEmpty />
      <WaitingForTraining />
      <List title="Recently completed" empty="Nothing completed yet." state="active" action="View" />
    </div>
  );
}

function List({ title, empty, state, action, hideWhenEmpty }: {
  title: string;
  empty: string;
  state: DraftState;
  action: string;
  hideWhenEmpty?: boolean;
}) {
  const [q, setQ] = useState('');
  const [limit, setLimit] = useState(PAGE);
  const drafts = useQuery({
    queryKey: ['drafts', state, q, limit],
    queryFn: () => listDrafts({ state, q: q || undefined, limit }),
    placeholderData: keepPreviousData,
  });
  const items = drafts.data?.items ?? [];
  const total = drafts.data?.total ?? 0;
  if (hideWhenEmpty && total === 0 && !q) return null;
  const to = (d: EmploymentDraft) => (state === 'draft' && (d.roles?.length ?? 0) === 0 ? 'access' : 'review');

  return (
    <section className="rounded-lg border border-border bg-card">
      <header className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
        <span className="font-semibold">{title}</span>
        {total > 0 && <span className="text-xs text-muted-foreground">{total}</span>}
        <SearchInput value={q} onChange={(v) => { setQ(v); setLimit(PAGE); }}
          placeholder="Name or email…" className="ml-auto w-72" />
      </header>
      <ul className="divide-y divide-border">
        {items.map((d) => (
          <li key={d.id} className="flex items-center justify-between gap-3 px-4 py-3 text-sm">
            <div className="min-w-0">
              <div className="font-medium">{d.person_name || 'Unnamed draft'}</div>
              <div className="truncate text-xs text-muted-foreground">
                {[d.department_name, new Date(d.updated_at).toLocaleDateString()].filter(Boolean).join(' · ')}
              </div>
            </div>
            <Link to={`/onboarding/$id/${to(d)}`} params={{ id: d.id }} className="shrink-0 text-primary hover:underline">
              {action}
            </Link>
          </li>
        ))}
        {!drafts.isLoading && items.length === 0 && (
          <li className="px-4 py-5 text-sm text-muted-foreground">{q ? 'No one matches.' : empty}</li>
        )}
      </ul>
      {total > items.length && (
        <div className="border-t border-border px-4 py-2">
          <button type="button" onClick={() => setLimit((l) => l + PAGE)} className="text-sm text-primary hover:underline">
            Show more ({total - items.length})
          </button>
        </div>
      )}
    </section>
  );
}
