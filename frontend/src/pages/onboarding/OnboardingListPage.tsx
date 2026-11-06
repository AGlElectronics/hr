import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';

import { listDepartments } from '@/api/departments';
import { listDrafts, type EmploymentDraft } from '@/api/drafts';
import { listPeople, type Person } from '@/api/people';
import { SearchInput } from '@/components/SearchInput';
import { WaitingForTraining } from '@/components/WaitingForTraining';
import { matches } from '@/lib/search';

const PAGE = 10;

type Row = { id: string; name: string; sub: string; to: 'access' | 'review'; action: string; search: string[] };

export function OnboardingListPage() {
  const drafts = useQuery({ queryKey: ['drafts'], queryFn: () => listDrafts() });
  const people = useQuery({ queryKey: ['people'], queryFn: () => listPeople() });
  const departments = useQuery({ queryKey: ['departments'], queryFn: listDepartments });

  const byId = new Map((people.data?.items ?? []).map((p) => [p.id, p]));
  const deptName = new Map((departments.data?.items ?? []).map((d) => [d.id, d.name]));
  const onboardings = (drafts.data?.items ?? []).filter((d) => d.department_id);

  const row = (d: EmploymentDraft, sub: string, to: Row['to'], action: string): Row => {
    const p: Person | undefined = byId.get(d.person_id);
    const dept = deptName.get(d.department_id!) ?? '';
    const legal = p ? `${p.first_name} ${p.last_name}`.trim() : '';
    return {
      id: d.id,
      name: (legal || p?.email || 'Unnamed draft') + (p?.nickname ? ` “${p.nickname}”` : ''),
      sub: [dept, sub].filter(Boolean).join(' · '),
      to,
      action,
      search: [legal, p?.nickname ?? '', p?.username ?? '', p?.email ?? '', dept],
    };
  };

  const open = onboardings
    .filter((d) => d.state === 'draft')
    .map((d) => row(d, `updated ${new Date(d.updated_at).toLocaleDateString()}`, (d.roles?.length ?? 0) > 0 ? 'review' : 'access', 'Continue'));
  const completed = onboardings
    .filter((d) => d.state === 'active')
    .map((d) => row(d, new Date(d.updated_at).toLocaleDateString(), 'review', 'View'));

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

      <List title="In progress" empty="No onboardings in progress." items={open} />
      <WaitingForTraining />
      <List title="Recently completed" empty="Nothing completed yet." items={completed} paged />
    </div>
  );
}

function List({ title, empty, items, paged }: { title: string; empty: string; items: Row[]; paged?: boolean }) {
  const [q, setQ] = useState('');
  const [limit, setLimit] = useState(PAGE);
  const shown = items.filter((i) => matches(q, ...i.search));
  const visible = paged ? shown.slice(0, limit) : shown;

  return (
    <section className="rounded-lg border border-border bg-card">
      <header className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
        <span className="font-semibold">{title}</span>
        {items.length > 0 && (
          <SearchInput value={q} onChange={(v) => { setQ(v); setLimit(PAGE); }}
            placeholder="Name, nickname, email or department…" className="ml-auto w-72" />
        )}
      </header>
      <ul className="divide-y divide-border">
        {visible.map((i) => (
          <li key={i.id} className="flex items-center justify-between gap-3 px-4 py-3 text-sm">
            <div className="min-w-0">
              <div className="font-medium">{i.name}</div>
              <div className="truncate text-xs text-muted-foreground">{i.sub}</div>
            </div>
            <Link to={`/onboarding/$id/${i.to}`} params={{ id: i.id }} className="shrink-0 text-primary hover:underline">
              {i.action}
            </Link>
          </li>
        ))}
        {items.length === 0 && <li className="px-4 py-5 text-sm text-muted-foreground">{empty}</li>}
        {items.length > 0 && shown.length === 0 && <li className="px-4 py-5 text-sm text-muted-foreground">No one matches.</li>}
      </ul>
      {paged && shown.length > limit && (
        <div className="border-t border-border px-4 py-2">
          <button type="button" onClick={() => setLimit((l) => l + PAGE)} className="text-sm text-primary hover:underline">
            Show more ({shown.length - limit})
          </button>
        </div>
      )}
    </section>
  );
}
