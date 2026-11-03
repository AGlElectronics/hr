import { useState } from 'react';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Link, useNavigate } from '@tanstack/react-router';
import { AlertTriangle, ArrowRight, Building2, Search, UserPlus } from 'lucide-react';

import { getDashboard, type AttentionItem, type DepartmentSummary } from '@/api/dashboard';
import { listDrafts } from '@/api/drafts';
import { listPeople, PERSON_STATUSES } from '@/api/people';
import { PersonQuickPanel } from '@/components/PersonQuickPanel';
import { PersonStatusBadge } from '@/components/PersonStatusBadge';

const inputCls = 'w-full rounded-md border border-input bg-background px-3 py-2 text-sm';

export function DashboardPage() {
  const dash = useQuery({ queryKey: ['dashboard'], queryFn: getDashboard });
  const [open, setOpen] = useState<string | null>(null);
  const [quickDept, setQuickDept] = useState('');

  return (
    <div className="p-6 space-y-6">
      <header>
        <h1 className="text-2xl font-bold">HR dashboard</h1>
        <p className="text-sm text-muted-foreground">Start onboardings, find people and fix what needs attention — all from here.</p>
      </header>

      {dash.error && <p className="text-destructive">Failed to load: {(dash.error as Error).message}</p>}

      <section className="grid grid-cols-2 gap-3 md:grid-cols-5">
        {PERSON_STATUSES.map((s) => (
          <Link key={s.value} to="/people" className="rounded-lg border border-border bg-card p-3 hover:border-primary/50">
            <div className="text-xs uppercase tracking-wider text-muted-foreground">{s.label}</div>
            <div className="mt-1 text-2xl font-bold">{dash.data?.status_counts[s.value] ?? 0}</div>
          </Link>
        ))}
      </section>

      <div className="grid gap-6 lg:grid-cols-2">
        <QuickStart department={quickDept} onDepartment={setQuickDept} departments={dash.data?.departments ?? []} />
        <PeopleSearch onOpen={setOpen} />
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <Attention items={dash.data?.attention ?? []} loading={dash.isLoading} onOpen={setOpen} />
        <InProgress />
      </div>

      <Departments items={dash.data?.departments ?? []} onQuickStart={(id) => {
        setQuickDept(id);
        window.scrollTo({ top: 0, behavior: 'smooth' });
      }} />

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

function QuickStart({ department, onDepartment, departments }: {
  department: string;
  onDepartment: (id: string) => void;
  departments: DepartmentSummary[];
}) {
  const navigate = useNavigate();
  const [first, setFirst] = useState('');
  const [last, setLast] = useState('');
  const [email, setEmail] = useState('');
  const term = email.includes('@') ? email : `${first} ${last}`.trim();
  const matches = useQuery({
    queryKey: ['people', term, ''],
    queryFn: () => listPeople({ q: term }),
    enabled: term.length >= 3,
    placeholderData: keepPreviousData,
  });
  const existing = term.length >= 3 ? (matches.data?.items ?? []).slice(0, 3) : [];

  return (
    <Panel title="Quick start onboarding" icon={<UserPlus className="h-4 w-4 text-primary" />}>
      <form className="space-y-3" onSubmit={(e) => {
        e.preventDefault();
        void navigate({
          to: '/onboarding/new',
          search: { department: department || undefined, first_name: first || undefined, last_name: last || undefined, email: email || undefined },
        });
      }}>
        <select className={inputCls} value={department} onChange={(e) => onDepartment(e.target.value)} aria-label="Department">
          <option value="">Department…</option>
          {departments.map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
        </select>
        <div className="grid grid-cols-2 gap-2">
          <input className={inputCls} placeholder="First name" value={first} onChange={(e) => setFirst(e.target.value)} />
          <input className={inputCls} placeholder="Last name" value={last} onChange={(e) => setLast(e.target.value)} />
        </div>
        <input className={inputCls} type="email" placeholder="Private email (login)" value={email}
          onChange={(e) => setEmail(e.target.value)} />
        {existing.length > 0 && (
          <div className="rounded-md border border-amber-300 bg-amber-50 p-2 text-xs text-amber-900">
            <p className="mb-1 font-medium">Already known to HR?</p>
            {existing.map((p) => (
              <Link key={p.id} to="/onboarding/new" search={{ person: p.id, department: department || undefined }}
                className="flex items-center justify-between rounded px-1 py-0.5 hover:bg-amber-100">
                <span>{p.display_name} · {p.email_missing ? 'no email' : p.email}</span>
                <span>Onboard them →</span>
              </Link>
            ))}
          </div>
        )}
        <button type="submit" disabled={!department || !first.trim()}
          className="inline-flex items-center gap-2 rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-60">
          Continue to details <ArrowRight className="h-4 w-4" />
        </button>
        <p className="text-xs text-muted-foreground">Opens step 1 with these filled in; the rest of the profile is entered there.</p>
      </form>
    </Panel>
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
    <Panel title="Find someone" icon={<Search className="h-4 w-4 text-primary" />}
      action={<Link to="/people" className="text-sm text-primary hover:underline">All people</Link>}>
      <input className={inputCls} autoFocus placeholder="Name, nickname or email…" value={q} onChange={(e) => setQ(e.target.value)} />
      <ul className="mt-2 divide-y divide-border">
        {items.map((p) => (
          <li key={p.id}>
            <button type="button" onClick={() => onOpen(p.id)}
              className="flex w-full items-center justify-between gap-2 px-1 py-2 text-left text-sm hover:bg-muted/40">
              <span className="min-w-0">
                <span className="font-medium">{p.display_name}</span>
                <span className="block truncate text-xs text-muted-foreground">{p.email_missing ? 'Email missing' : p.email}</span>
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

const KIND_LABEL: Record<AttentionItem['kind'], string> = {
  missing_email: 'Missing email',
  incomplete_profile: 'Incomplete profile',
  failed_provisioning: 'Provisioning failed',
  stalled_onboarding: 'Stalled onboarding',
};

function Attention({ items, loading, onOpen }: { items: AttentionItem[]; loading: boolean; onOpen: (id: string) => void }) {
  return (
    <Panel title={`Needs attention${items.length ? ` (${items.length})` : ''}`} icon={<AlertTriangle className="h-4 w-4 text-amber-600" />}>
      {loading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {!loading && items.length === 0 && <p className="text-sm text-muted-foreground">Nothing to fix right now.</p>}
      <ul className="max-h-80 divide-y divide-border overflow-y-auto">
        {items.map((it) => (
          <li key={`${it.kind}-${it.person_id}-${it.draft_id ?? ''}`} className="flex items-start justify-between gap-3 py-2 text-sm">
            <div className="min-w-0">
              <span className="mr-2 rounded-pill bg-amber-100 px-2 py-0.5 text-xs text-amber-900">{KIND_LABEL[it.kind]}</span>
              <span className="font-medium">{it.display_name}</span>
              <p className="truncate text-xs text-muted-foreground" title={it.detail}>{it.detail}</p>
            </div>
            {it.draft_id ? (
              <Link to="/onboarding/$id/review" params={{ id: it.draft_id }} className="shrink-0 text-primary hover:underline">Open</Link>
            ) : (
              <span className="flex shrink-0 gap-3">
                <Link to="/people/$id" params={{ id: it.person_id }} search={{ edit: true }} className="text-primary hover:underline">Fix</Link>
                <button type="button" onClick={() => onOpen(it.person_id)} className="text-muted-foreground hover:underline">View</button>
              </span>
            )}
          </li>
        ))}
      </ul>
    </Panel>
  );
}

function InProgress() {
  const drafts = useQuery({ queryKey: ['drafts'], queryFn: () => listDrafts() });
  const items = (drafts.data?.items ?? []).filter((d) => d.state === 'draft' && d.department_id).slice(0, 8);
  return (
    <Panel title="Onboardings in progress" icon={<UserPlus className="h-4 w-4 text-primary" />}
      action={<Link to="/onboarding" className="text-sm text-primary hover:underline">All</Link>}>
      {items.length === 0 && <p className="text-sm text-muted-foreground">None in progress.</p>}
      <ul className="divide-y divide-border">
        {items.map((d) => (
          <li key={d.id} className="flex items-center justify-between py-2 text-sm">
            <span>
              {d.title}
              <span className="block text-xs text-muted-foreground">updated {new Date(d.updated_at).toLocaleDateString()}</span>
            </span>
            <span className="flex gap-3">
              <Link to="/onboarding/$id/access" params={{ id: d.id }} className="text-muted-foreground hover:underline">Access</Link>
              <Link to="/onboarding/$id/review" params={{ id: d.id }} className="text-primary hover:underline">Review →</Link>
            </span>
          </li>
        ))}
      </ul>
    </Panel>
  );
}

function Departments({ items, onQuickStart }: { items: DepartmentSummary[]; onQuickStart: (id: string) => void }) {
  return (
    <Panel title="Departments" icon={<Building2 className="h-4 w-4 text-primary" />}
      action={<Link to="/departments" className="text-sm text-primary hover:underline">Manage</Link>}>
      {items.length === 0 && (
        <p className="text-sm text-muted-foreground">
          No departments yet. <Link to="/departments" className="text-primary hover:underline">Create one</Link>.
        </p>
      )}
      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
        {items.map((d) => (
          <div key={d.id} className="rounded-md border border-border p-3">
            <div className="flex items-start justify-between gap-2">
              <div className="min-w-0">
                <p className="font-medium">{d.name}</p>
                <p className="font-mono text-xs text-muted-foreground">{d.group_name}</p>
              </div>
              <div className="text-right">
                <p className="text-xl font-bold leading-none">{d.active_members}</p>
                <p className="text-[11px] text-muted-foreground">active</p>
              </div>
            </div>
            <div className="mt-2 flex flex-wrap gap-1">
              {d.default_roles.length === 0 && <span className="text-xs text-muted-foreground">No default access</span>}
              {d.default_roles.map((r) => (
                <span key={r.role_name} className="rounded-pill bg-primary-soft px-2 py-0.5 text-xs text-primary">{r.role_name}</span>
              ))}
            </div>
            {d.idp_sync_error && <p className="mt-1 text-xs text-destructive">Keycloak: {d.idp_sync_error}</p>}
            <div className="mt-2 flex items-center justify-between text-xs">
              <span className="text-muted-foreground">{d.in_onboarding} in onboarding</span>
              <button type="button" onClick={() => onQuickStart(d.id)} className="text-primary hover:underline">Onboard into →</button>
            </div>
          </div>
        ))}
      </div>
    </Panel>
  );
}
