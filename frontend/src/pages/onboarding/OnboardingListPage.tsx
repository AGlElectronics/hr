import { useState } from 'react';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';

import { listDrafts, type DraftState, type EmploymentDraft } from '@/api/drafts';
import { SearchInput } from '@/components/SearchInput';
import { WaitingForTraining } from '@/components/WaitingForTraining';

const PAGE = 10;

function statusLabel(d: EmploymentDraft): string {
  if (d.ready_to_approve) return 'Ready to approve';
  if (d.state === 'pending_approval') return 'Waiting for signatures';
  if (d.state === 'draft') return 'Editing';
  if (d.state === 'provisioning') return 'Setting up account';
  if (d.state === 'active') return 'Active';
  if (d.state === 'rejected') return 'Cancelled';
  return d.state;
}

export function OnboardingListPage() {
  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold">Onboarding</h1>
          <p className="text-sm text-muted-foreground">
            Give an approved volunteer their account: details, access, forms, then Approve after signatures.
            Signed packets show under Ready to approve here — not under Review queue (that queue is for IdP accounts without an HR person).
          </p>
        </div>
        <Link to="/onboarding/new" className="shrink-0 rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground">
          Start onboarding
        </Link>
      </div>

      <List
        title="Ready to approve"
        empty="No onboardings waiting for your Approve."
        states={['pending_approval']}
        onlyReady
        action={() => 'Approve'}
      />
      <List
        title="In progress"
        empty="No onboardings in progress."
        states={['draft', 'pending_approval']}
        excludeReady
        action={(d) => (d.state === 'pending_approval' ? 'Open' : 'Continue')}
      />
      <List title="Being set up" empty="" states={['provisioning']} action={() => 'View'} hideWhenEmpty />
      <WaitingForTraining />
      <List title="Recently completed" empty="Nothing completed yet." states={['active']} action={() => 'View'} />
    </div>
  );
}

function List({
  title,
  empty,
  states,
  action,
  hideWhenEmpty,
  onlyReady,
  excludeReady,
}: {
  title: string;
  empty: string;
  states: DraftState[];
  action: (d: EmploymentDraft) => string;
  hideWhenEmpty?: boolean;
  onlyReady?: boolean;
  excludeReady?: boolean;
}) {
  const [q, setQ] = useState('');
  const [limit, setLimit] = useState(PAGE);
  const stateParam = states.join(',');
  const drafts = useQuery({
    queryKey: ['drafts', stateParam, q, limit, onlyReady, excludeReady],
    // Fetch a wider page when we split pending_approval client-side by ready_to_approve.
    queryFn: () =>
      listDrafts({
        state: stateParam,
        q: q || undefined,
        limit: onlyReady || excludeReady ? Math.max(limit, 50) : limit,
      }),
    placeholderData: keepPreviousData,
  });
  const raw = drafts.data?.items ?? [];
  const items = raw.filter((d) => {
    if (onlyReady) return !!d.ready_to_approve;
    if (excludeReady) return !d.ready_to_approve;
    return true;
  });
  // When we filter client-side, prefer the visible count for the badge.
  const total = onlyReady || excludeReady ? items.length : (drafts.data?.total ?? 0);
  if (hideWhenEmpty && total === 0 && !q) return null;
  const to = (d: EmploymentDraft) =>
    d.state === 'draft' && (d.roles?.length ?? 0) === 0 ? 'access' : 'review';

  return (
    <section className="rounded-lg border border-border bg-card">
      <header className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
        <span className="font-semibold">{title}</span>
        {total > 0 && <span className="text-xs text-muted-foreground">{total}</span>}
        <SearchInput
          value={q}
          onChange={(v) => {
            setQ(v);
            setLimit(PAGE);
          }}
          placeholder="Name or email…"
          className="ml-auto w-72"
        />
      </header>
      <ul className="divide-y divide-border">
        {items.map((d) => (
          <li key={d.id} className="flex items-center justify-between gap-3 px-4 py-3 text-sm">
            <div className="min-w-0">
              <div className="font-medium">{d.person_name || 'Unnamed draft'}</div>
              <div className="truncate text-xs text-muted-foreground">
                {[statusLabel(d), d.department_name, new Date(d.updated_at).toLocaleDateString()]
                  .filter(Boolean)
                  .join(' · ')}
              </div>
            </div>
            <Link to={`/onboarding/$id/${to(d)}`} params={{ id: d.id }} className="shrink-0 text-primary hover:underline">
              {action(d)}
            </Link>
          </li>
        ))}
        {!drafts.isLoading && items.length === 0 && (
          <li className="px-4 py-5 text-sm text-muted-foreground">{q ? 'No one matches.' : empty}</li>
        )}
      </ul>
      {!onlyReady && !excludeReady && (drafts.data?.total ?? 0) > raw.length && (
        <div className="border-t border-border px-4 py-2">
          <button type="button" onClick={() => setLimit((l) => l + PAGE)} className="text-sm text-primary hover:underline">
            Show more ({(drafts.data?.total ?? 0) - raw.length})
          </button>
        </div>
      )}
    </section>
  );
}
