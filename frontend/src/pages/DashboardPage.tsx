import { useEffect, useState } from 'react';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Link, useNavigate } from '@tanstack/react-router';
import { AlertTriangle, ArrowRight, Search, UserPlus, X } from 'lucide-react';

import { getDashboard, type AttentionItem, type AttentionKind, type DepartmentSummary } from '@/api/dashboard';
import { listDrafts } from '@/api/drafts';
import { listPeople, PERSON_STATUSES, type PersonSummary } from '@/api/people';
import { PersonQuickPanel } from '@/components/PersonQuickPanel';
import { PersonStatusBadge } from '@/components/PersonStatusBadge';
import { SearchInput } from '@/components/SearchInput';
import { matches } from '@/lib/search';
import { noAutofill } from '@/lib/noAutofill';
import { useRoleLabels } from '@/lib/roles';
import { cn } from '@/lib/utils';

const inputCls = 'w-full rounded-md border border-input bg-background px-3 py-2 text-sm';

export function DashboardPage() {
  const dash = useQuery({ queryKey: ['dashboard'], queryFn: getDashboard });
  const [open, setOpen] = useState<string | null>(null);

  return (
    <div className="p-6 space-y-6">
      <header>
        <h1 className="text-2xl font-bold">HR dashboard</h1>
        <p className="text-sm text-muted-foreground">Start onboardings, find people and fix what needs attention, all from here.</p>
      </header>

      {dash.error && <p className="text-destructive">Failed to load: {(dash.error as Error).message}</p>}

      <section className="grid grid-cols-2 gap-3 md:grid-cols-4">
        {PERSON_STATUSES.map((s) => (
          <Link key={s.value} to="/people" search={{ status: s.value }} title={s.hint}
            className="rounded-lg border border-border bg-card p-3 hover:border-primary/50">
            <div className="text-xs uppercase tracking-wider text-muted-foreground">{s.label}</div>
            <div className="mt-1 text-2xl font-bold">{dash.data?.status_counts[s.value] ?? 0}</div>
          </Link>
        ))}
      </section>

      <div className="grid items-start gap-6 lg:grid-cols-2">
        <QuickStart departments={dash.data?.departments ?? []} />
        <PeopleSearch onOpen={setOpen} />
      </div>

      <div className="grid items-start gap-6 lg:grid-cols-2">
        <Attention items={dash.data?.attention ?? []} loading={dash.isLoading} onOpen={setOpen} />
        <InProgress />
      </div>

      {open && <PersonQuickPanel personId={open} onClose={() => setOpen(null)} />}
    </div>
  );
}

function Panel({ title, icon, action, children }: {
  title: string;
  icon?: React.ReactNode;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-lg border border-border bg-card">
      <header className="flex items-center justify-between gap-2 border-b border-border px-4 py-3">
        <h2 className="inline-flex items-center gap-2 font-semibold">{icon}{title}</h2>
        {action}
      </header>
      <div className="p-4">{children}</div>
    </section>
  );
}

function ViewAll({ to }: { to: '/people' | '/onboarding' }) {
  return <Link to={to} className="text-sm text-primary hover:underline">View all →</Link>;
}

function QuickStart({ departments }: { departments: DepartmentSummary[] }) {
  const navigate = useNavigate();
  const [department, setDepartment] = useState('');
  const [first, setFirst] = useState('');
  const [last, setLast] = useState('');
  const [email, setEmail] = useState('');
  const term = email.includes('@') ? email : `${first} ${last}`.trim();
  const found = useQuery({
    queryKey: ['people', term, ''],
    queryFn: () => listPeople({ q: term }),
    enabled: term.length >= 3,
    placeholderData: keepPreviousData,
  });
  const existing = term.length >= 3 ? (found.data?.items ?? []).slice(0, 3) : [];

  return (
    <Panel title="Quick start onboarding" icon={<UserPlus className="h-4 w-4 text-primary" />}>
      <form className="space-y-3" autoComplete="off" onSubmit={(e) => {
        e.preventDefault();
        void navigate({
          to: '/onboarding/new',
          search: { department: department || undefined, first_name: first || undefined, last_name: last || undefined, email: email || undefined },
        });
      }}>
        <select className={inputCls} value={department} onChange={(e) => setDepartment(e.target.value)} aria-label="Department">
          <option value="">Department…</option>
          {departments.map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
        </select>
        <div className="grid grid-cols-2 gap-2">
          <input className={inputCls} {...noAutofill} placeholder="First name" value={first} onChange={(e) => setFirst(e.target.value)} />
          <input className={inputCls} {...noAutofill} placeholder="Last name" value={last} onChange={(e) => setLast(e.target.value)} />
        </div>
        <input className={inputCls} {...noAutofill} type="email" placeholder="Private email" value={email}
          onChange={(e) => setEmail(e.target.value)} />
        <button type="submit" disabled={!department || !first.trim()}
          className="inline-flex items-center gap-2 rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-60">
          Continue to details <ArrowRight className="h-4 w-4" />
        </button>
        <p className="text-xs text-muted-foreground">Opens step 1 with these filled in; the rest of the profile is entered there.</p>
      </form>
      <KnownToHR people={existing} term={term} department={department} />
    </Panel>
  );
}

/** Bottom pop-up shown while the quick start matches someone HR already has. */
function KnownToHR({ people, term, department }: { people: PersonSummary[]; term: string; department: string }) {
  const [dismissed, setDismissed] = useState('');
  useEffect(() => {
    if (people.length === 0) setDismissed('');
  }, [people.length]);
  if (people.length === 0 || dismissed === term) return null;
  return (
    <div role="status" className="fixed inset-x-0 bottom-4 z-40 mx-auto w-[min(36rem,calc(100%-2rem))] rounded-lg border border-warning/40 bg-card p-3 shadow-lg">
      <div className="mb-1 flex items-center justify-between gap-2">
        <p className="flex items-center gap-2 text-sm font-semibold">
          <AlertTriangle className="h-4 w-4 text-warning" /> Already known to HR?
        </p>
        <button type="button" onClick={() => setDismissed(term)} aria-label="Dismiss" className="rounded-pill p-1 hover:bg-muted">
          <X className="h-4 w-4" />
        </button>
      </div>
      <ul className="divide-y divide-border">
        {people.map((p) => (
          <li key={p.id}>
            <Link to="/onboarding/new" search={{ person: p.id, department: department || undefined }}
              className="flex items-center justify-between gap-3 rounded px-1 py-1.5 text-sm hover:bg-muted/60">
              <span className="min-w-0 truncate">
                <span className="font-medium">{p.display_name}</span>
                <span className="text-muted-foreground"> · {p.email}</span>
              </span>
              <span className="shrink-0 text-primary">Onboard them →</span>
            </Link>
          </li>
        ))}
      </ul>
    </div>
  );
}

function PeopleSearch({ onOpen }: { onOpen: (id: string) => void }) {
  const [q, setQ] = useState('');
  const results = useQuery({
    queryKey: ['people', q, ''],
    queryFn: () => listPeople({ q }),
    enabled: q.trim().length >= 2,
    placeholderData: keepPreviousData,
  });
  const items = q.trim().length >= 2 ? (results.data?.items ?? []).slice(0, 8) : [];

  return (
    <Panel title="Find someone" icon={<Search className="h-4 w-4 text-primary" />} action={<ViewAll to="/people" />}>
      <SearchInput value={q} onChange={setQ} placeholder="Name, nickname or email…" />
      <ul className="mt-2 divide-y divide-border">
        {items.map((p) => (
          <li key={p.id}>
            <button type="button" onClick={() => onOpen(p.id)}
              className="flex w-full items-center justify-between gap-2 px-1 py-2 text-left text-sm hover:bg-muted/40">
              <span className="min-w-0">
                <span className="font-medium">{p.display_name}</span>
                <span className="block truncate text-xs text-muted-foreground">{p.email}</span>
              </span>
              <PersonStatusBadge status={p.status} />
            </button>
          </li>
        ))}
        {q.trim().length >= 2 && !results.isFetching && items.length === 0 && (
          <li className="py-2 text-sm text-muted-foreground">No one matches.</li>
        )}
      </ul>
      {q.trim().length < 2 && <p className="mt-2 text-xs text-muted-foreground">Click a result to edit, change access, pause or offboard.</p>}
    </Panel>
  );
}

const KIND_LABEL: Record<AttentionKind, string> = {
  incomplete_profile: 'Incomplete profile',
  failed_provisioning: 'Account setup failed',
  stalled_onboarding: 'Stalled onboarding',
};

const PAGE = 10;

function Attention({ items, loading, onOpen }: { items: AttentionItem[]; loading: boolean; onOpen: (id: string) => void }) {
  const [q, setQ] = useState('');
  const [kind, setKind] = useState<AttentionKind | ''>('');
  const [limit, setLimit] = useState(PAGE);
  const counts = items.reduce<Partial<Record<AttentionKind, number>>>((m, it) => ({ ...m, [it.kind]: (m[it.kind] ?? 0) + 1 }), {});
  const shown = items.filter((it) => (!kind || it.kind === kind) && matches(q, it.display_name, it.legal_name, it.email, it.detail));

  return (
    <Panel title={`Needs attention${items.length ? ` (${items.length})` : ''}`} icon={<AlertTriangle className="h-4 w-4 text-warning" />}>
      {loading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {!loading && items.length === 0 && <p className="text-sm text-muted-foreground">Nothing to fix right now.</p>}
      {items.length > 0 && (
        <div className="mb-2 space-y-2">
          <SearchInput value={q} onChange={(v) => { setQ(v); setLimit(PAGE); }} placeholder="Search name or email…" />
          <div className="flex flex-wrap gap-1.5">
            <Chip on={!kind} onClick={() => setKind('')}>All</Chip>
            {(Object.keys(KIND_LABEL) as AttentionKind[]).filter((k) => counts[k]).map((k) => (
              <Chip key={k} on={kind === k} onClick={() => setKind(k)}>{KIND_LABEL[k]} ({counts[k]})</Chip>
            ))}
          </div>
        </div>
      )}
      <ul className="divide-y divide-border">
        {shown.slice(0, limit).map((it) => <AttentionRow key={`${it.kind}-${it.person_id}-${it.draft_id ?? ''}`} it={it} onOpen={onOpen} />)}
        {items.length > 0 && shown.length === 0 && <li className="py-2 text-sm text-muted-foreground">No one matches.</li>}
      </ul>
      {shown.length > limit && (
        <button type="button" onClick={() => setLimit((l) => l + PAGE)} className="mt-2 text-sm text-primary hover:underline">
          Show more ({shown.length - limit})
        </button>
      )}
    </Panel>
  );
}

function Chip({ on, onClick, children }: { on: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button type="button" onClick={onClick} aria-pressed={on}
      className={cn('rounded-pill border px-2.5 py-0.5 text-xs', on ? 'border-primary bg-primary-soft text-primary' : 'border-border text-muted-foreground hover:text-foreground')}>
      {children}
    </button>
  );
}

function AttentionRow({ it, onOpen }: { it: AttentionItem; onOpen: (id: string) => void }) {
  const [details, setDetails] = useState(false);
  const fix =
    it.kind === 'incomplete_profile' || !it.draft_id ? (
      <Link to="/people/$id" params={{ id: it.person_id }} search={{ edit: true }} className="text-primary hover:underline">Fix</Link>
    ) : (
      <Link to={it.kind === 'failed_provisioning' ? '/onboarding/$id/review' : '/onboarding/$id/details'} params={{ id: it.draft_id }}
        className="text-primary hover:underline">Fix</Link>
    );
  const hint = it.display_name !== it.legal_name ? it.legal_name : it.email;
  return (
    <li className="grid grid-cols-[1fr_auto] items-start gap-x-3 py-2 text-sm">
      <div className="min-w-0">
        <p className="truncate">
          <span className="font-medium">{it.display_name}</span>
          {hint && <span className="text-xs text-muted-foreground"> · {hint}</span>}
        </p>
        <p className="mt-0.5 flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
          <span className="rounded-pill bg-warning-soft px-2 py-0.5 text-warning-foreground">{KIND_LABEL[it.kind]}</span>
          <span title={it.fields?.join(', ')}>{it.detail}</span>
          {it.technical && (
            <button type="button" onClick={() => setDetails((d) => !d)} className="text-primary hover:underline">
              {details ? 'Hide details' : 'Details'}
            </button>
          )}
        </p>
        {details && it.technical && (
          <pre className="mt-1 whitespace-pre-wrap break-words rounded bg-muted p-2 font-mono text-[11px]">{it.technical}</pre>
        )}
      </div>
      <span className="flex shrink-0 gap-3 pt-0.5">
        {fix}
        <button type="button" onClick={() => onOpen(it.person_id)} className="text-muted-foreground hover:underline">View</button>
      </span>
    </li>
  );
}

function InProgress() {
  const drafts = useQuery({ queryKey: ['drafts', 'draft', 'dashboard'], queryFn: () => listDrafts({ state: 'draft', limit: 8 }) });
  const roles = useRoleLabels();
  const items = (drafts.data?.items ?? []).filter((d) => d.department_id);

  return (
    <Panel title="Onboardings in progress" icon={<UserPlus className="h-4 w-4 text-primary" />} action={<ViewAll to="/onboarding" />}>
      {items.length === 0 && <p className="text-sm text-muted-foreground">None in progress.</p>}
      <ul className="divide-y divide-border">
        {items.map((d) => {
          const access = roles.grouped((d.roles ?? []).map((r) => r.role_name));
          return (
            <li key={d.id} className="flex items-center justify-between gap-3 py-2 text-sm">
              <span className="min-w-0">
                <span className="font-medium">{d.person_name || 'Unnamed draft'}</span>
                <span className="block truncate text-xs text-muted-foreground">
                  {[d.department_name, access].filter(Boolean).join(' · ')}
                </span>
              </span>
              <Link to={(d.roles?.length ?? 0) > 0 ? '/onboarding/$id/review' : '/onboarding/$id/access'} params={{ id: d.id }}
                className="shrink-0 text-primary hover:underline">Continue →</Link>
            </li>
          );
        })}
      </ul>
    </Panel>
  );
}
