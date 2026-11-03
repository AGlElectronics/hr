import { useMemo, useState, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useParams } from '@tanstack/react-router';
import { AlertTriangle, CheckCircle2, Mail, Pencil } from 'lucide-react';

import { getAccessCatalog, roleRef } from '@/api/catalog';
import { completeOnboarding, getOnboarding, type CompleteResult } from '@/api/onboarding';
import { SOCIAL_NETWORKS } from '@/api/people';
import { OnboardingSteps } from '@/components/OnboardingSteps';

const FIELD_LABELS: Record<string, string> = {
  first_name: 'First name',
  last_name: 'Last name',
  nickname: 'Nickname',
  date_of_birth: 'Date of birth',
  email: 'Private email',
  phone: 'Phone',
  address: 'Address',
  'emergency_contact.name': 'Emergency contact name',
  'emergency_contact.phone': 'Emergency contact phone',
};

/** Step 3: verify everything, then complete (account + groups + set-password email). */
export function OnboardingReviewPage() {
  const { id } = useParams({ from: '/app/onboarding/$id/review' });
  const qc = useQueryClient();
  const view = useQuery({ queryKey: ['onboarding', id], queryFn: () => getOnboarding(id) });
  const catalog = useQuery({ queryKey: ['access-catalog'], queryFn: getAccessCatalog });
  const [result, setResult] = useState<CompleteResult | null>(null);
  const [confirmed, setConfirmed] = useState(false);

  const roleLabels = useMemo(() => {
    const m = new Map<string, { module: string; label: string }>();
    for (const mod of catalog.data?.modules ?? [])
      for (const r of mod.roles) m.set(roleRef(mod.module, r.key), { module: mod.display_name, label: r.label });
    return m;
  }, [catalog.data]);

  const complete = useMutation({
    mutationFn: () => completeOnboarding(id),
    onSuccess: (res) => {
      setResult(res);
      void qc.invalidateQueries({ queryKey: ['onboarding', id] });
      void qc.invalidateQueries({ queryKey: ['people'] });
      void qc.invalidateQueries({ queryKey: ['drafts'] });
    },
  });

  if (view.isLoading) return <p className="p-6 text-muted-foreground">Loading…</p>;
  if (view.error || !view.data) return <p className="p-6 text-destructive">{(view.error as Error)?.message}</p>;
  const { draft, person: p, department, missing_fields } = view.data;
  const done = draft.state === 'active';
  const missing = Object.keys(missing_fields ?? {});
  const ec = p.emergency_contact ?? {};
  const socials = SOCIAL_NETWORKS.filter((n) => p.social_handles?.[n.key]);
  const groups = [department?.group_name, ...(draft.roles ?? []).map((r) => r.group_name)].filter(Boolean);

  return (
    <div className="p-6 max-w-4xl space-y-5">
      <Link to="/onboarding" className="text-sm text-muted-foreground hover:underline">← Onboarding</Link>
      <div>
        <h1 className="text-2xl font-bold">{done ? 'Onboarding completed' : 'Review & complete'}</h1>
        <p className="text-sm text-muted-foreground">
          {done
            ? `${p.display_name} has an account and is active.`
            : 'Check everything below. Nothing is created in Keycloak until you press Complete.'}
        </p>
      </div>
      <OnboardingSteps current="review" id={id} locked={done} />

      {result && <CompleteOutcome result={result} email={p.email} />}

      {missing.length > 0 && !done && (
        <div className="rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900">
          <p className="flex items-center gap-2 font-semibold">
            <AlertTriangle className="h-4 w-4" /> The volunteer profile is incomplete
          </p>
          <p className="mt-1">
            Missing: {missing.map((k) => FIELD_LABELS[k] ?? k).join(', ')}.{' '}
            <Link to="/onboarding/$id/details" params={{ id }} className="underline">Fix on page 1</Link>
          </p>
        </div>
      )}

      <Card title="Volunteer" editTo={done ? undefined : 'details'} id={id}>
        <Row label="Legal name" value={p.legal_name} />
        <Row label="Nickname" value={p.nickname} />
        <Row label="Date of birth" value={p.date_of_birth} />
        <Row label="Private email" value={p.email} hint="Login username; the set-password email goes here" />
        <Row label="Phone" value={p.phone} />
        <Row label="Address" value={p.address} />
        {socials.map((n) => (
          <Row key={n.key} label={n.label} value={p.social_handles[n.key]} />
        ))}
        <Row label="Emergency contact" value={[ec.name, ec.phone, ec.email].filter(Boolean).join(' · ')} />
        {ec.comments && <Row label="Emergency notes" value={ec.comments} />}
      </Card>

      <Card title="Department" editTo={done ? undefined : 'details'} id={id}>
        <Row label="Department" value={department?.name} />
        <Row label="Group" value={department?.group_name} mono />
      </Card>

      <Card title="Access" editTo={done ? undefined : 'access'} id={id}>
        {(draft.roles ?? []).length === 0 && <p className="text-sm text-muted-foreground">Only the department group.</p>}
        {(draft.roles ?? []).map((r) => {
          const hit = roleLabels.get(r.role_name);
          return (
            <Row
              key={r.id}
              label={hit?.module ?? r.role_name.split(':')[0]}
              value={`${hit?.label ?? r.role_name.split(':')[1] ?? r.role_name}${r.group_name ? `  →  ${r.group_name}` : ''}`}
            />
          );
        })}
      </Card>

      {!done && (
        <section className="rounded-lg border-2 border-primary/40 bg-card p-4 space-y-3">
          <h2 className="font-semibold">When you press Complete</h2>
          <ol className="list-decimal space-y-1 pl-5 text-sm">
            <li>
              A Keycloak account is created with username <strong>{p.email}</strong>.
            </li>
            <li>
              It is added to the groups: <span className="font-mono text-xs">{groups.join(', ') || '—'}</span>.
            </li>
            <li>
              {p.display_name} receives an email that their onboarding is complete, with a link to create their
              password. Afterwards they can sign in to the portal and the modules they have access to.
            </li>
            <li>Their status becomes Active.</li>
          </ol>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} />
            I verified the details above.
          </label>
          <button
            onClick={() => complete.mutate()}
            disabled={!confirmed || missing.length > 0 || !department || complete.isPending}
            className="rounded-pill bg-primary px-5 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-60"
          >
            {complete.isPending ? 'Creating account…' : 'Complete onboarding'}
          </button>
          {complete.error && <p className="text-sm text-destructive">{(complete.error as Error).message}</p>}
        </section>
      )}
    </div>
  );
}

function CompleteOutcome({ result, email }: { result: CompleteResult; email: string }) {
  if (!result.ok) {
    return (
      <div className="rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm">
        <p className="flex items-center gap-2 font-semibold text-destructive">
          <AlertTriangle className="h-4 w-4" /> Could not complete onboarding
        </p>
        <p className="mt-1">{result.error}</p>
        <p className="mt-1 text-muted-foreground">Nothing was finalized; the onboarding is still editable — fix and retry.</p>
      </div>
    );
  }
  return (
    <div className="rounded-md border border-emerald-300 bg-emerald-50 p-3 text-sm text-emerald-900 space-y-1">
      <p className="flex items-center gap-2 font-semibold">
        <CheckCircle2 className="h-4 w-4" />
        {result.account_created ? 'Account created' : 'Existing account updated'} · groups:{' '}
        {(result.groups_applied ?? []).join(', ')}
      </p>
      {result.password_setup_email_sent ? (
        <p className="flex items-center gap-2">
          <Mail className="h-4 w-4" /> Onboarding email with the set-password link sent to <strong>{email}</strong>.
        </p>
      ) : result.temporary_password ? (
        <p>
          The email could not be sent{result.password_setup_error ? ` (${result.password_setup_error})` : ''}. Give them
          this one-time password instead (they must change it on first login):{' '}
          <code className="rounded bg-white px-1.5 py-0.5 font-mono">{result.temporary_password}</code>
        </p>
      ) : (
        <p>They already had an account, so no password email was needed.</p>
      )}
    </div>
  );
}

function Card({ title, editTo, id, children }: { title: string; editTo?: 'details' | 'access'; id: string; children: ReactNode }) {
  return (
    <section className="rounded-lg border border-border bg-card p-4 space-y-2">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold">{title}</h2>
        {editTo && (
          <Link
            to={`/onboarding/$id/${editTo}`}
            params={{ id }}
            className="inline-flex items-center gap-1 text-xs text-primary hover:underline"
          >
            <Pencil className="h-3 w-3" /> Edit
          </Link>
        )}
      </div>
      {children}
    </section>
  );
}

function Row({ label, value, hint, mono }: { label: string; value?: string; hint?: string; mono?: boolean }) {
  return (
    <div className="grid grid-cols-[11rem_1fr] gap-2 text-sm">
      <span className="text-muted-foreground">{label}</span>
      <span className={mono ? 'font-mono text-xs' : 'break-words'}>
        {value || <span className="text-destructive">missing</span>}
        {hint && <span className="block text-xs text-muted-foreground">{hint}</span>}
      </span>
    </div>
  );
}
