import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';

import { listDepartments } from '@/api/departments';
import { listDrafts } from '@/api/drafts';
import { listPeople } from '@/api/people';

export function OnboardingListPage() {
  const drafts = useQuery({ queryKey: ['drafts'], queryFn: () => listDrafts() });
  const people = useQuery({ queryKey: ['people'], queryFn: () => listPeople() });
  const departments = useQuery({ queryKey: ['departments'], queryFn: listDepartments });

  const personName = new Map((people.data?.items ?? []).map((p) => [p.id, p.display_name]));
  const deptName = new Map((departments.data?.items ?? []).map((d) => [d.id, d.name]));
  const onboardings = (drafts.data?.items ?? []).filter((d) => d.department_id);
  const open = onboardings.filter((d) => d.state === 'draft');
  const completed = onboardings.filter((d) => d.state === 'active').slice(0, 10);

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold">Onboarding</h1>
          <p className="text-sm text-muted-foreground">
            Turn an approved application into an account: details → access → review.
          </p>
        </div>
        <Link to="/onboarding/new" className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground">
          Start onboarding
        </Link>
      </div>

      <List
        title="In progress"
        empty="No onboardings in progress."
        items={open.map((d) => ({
          id: d.id,
          name: personName.get(d.person_id) ?? '…',
          sub: deptName.get(d.department_id!) ?? '',
          to: (d.roles?.length ?? 0) > 0 ? 'review' : 'access',
          action: 'Continue',
        }))}
      />
      <List
        title="Recently completed"
        empty="Nothing completed yet."
        items={completed.map((d) => ({
          id: d.id,
          name: personName.get(d.person_id) ?? '…',
          sub: `${deptName.get(d.department_id!) ?? ''} · ${new Date(d.updated_at).toLocaleDateString()}`,
          to: 'review',
          action: 'View',
        }))}
      />
    </div>
  );
}

function List({
  title,
  empty,
  items,
}: {
  title: string;
  empty: string;
  items: { id: string; name: string; sub: string; to: 'access' | 'review'; action: string }[];
}) {
  return (
    <section className="rounded-lg border border-border bg-card">
      <header className="border-b border-border px-4 py-3 font-semibold">{title}</header>
      <ul className="divide-y divide-border">
        {items.map((i) => (
          <li key={i.id} className="flex items-center justify-between px-4 py-3 text-sm">
            <div>
              <div className="font-medium">{i.name}</div>
              <div className="text-xs text-muted-foreground">{i.sub}</div>
            </div>
            <Link to={`/onboarding/$id/${i.to}`} params={{ id: i.id }} className="text-primary hover:underline">
              {i.action}
            </Link>
          </li>
        ))}
        {items.length === 0 && <li className="px-4 py-5 text-sm text-muted-foreground">{empty}</li>}
      </ul>
    </section>
  );
}
