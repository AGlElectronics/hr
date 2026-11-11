import { useState, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from '@tanstack/react-router';
import { AlertTriangle, CheckCircle2, GraduationCap, Loader2, Mail, Pencil, XCircle } from 'lucide-react';

import { resendPasswordSetup } from '@/api/lifecycle';
import { cancelOnboarding, completeOnboarding, getOnboarding, type CompleteResult, type ProvisioningStatus } from '@/api/onboarding';
import { addressLine, SOCIAL_NETWORKS } from '@/api/people';
import { waitingText, type PendingGrant } from '@/api/roleGate';
import { OnboardingSteps } from '@/components/OnboardingSteps';
import { useDebugMode } from '@/lib/debug';
import { undeliverableText, useEmailCheck } from '@/lib/emailCheck';
import { formatPhone } from '@/lib/phone';
import { useRoleLabels } from '@/lib/roles';

const FIELD_LABELS: Record<string, string> = {
  first_name: 'First name',
  last_name: 'Last name',
  nickname: 'Nickname',
  date_of_birth: 'Date of birth',
  email: 'Private email',
  phone: 'Phone',
  'address.street': 'Street',
  'address.postal_code': 'Postal code',
  'address.city': 'City',
  'address.country': 'Country',
  'emergency_contact.name': 'Emergency contact name',
  'emergency_contact.phone': 'Emergency contact phone',
};

/**
 * Step 3: verify everything, then complete. Completing queues a background
 * job (account, access, set-password email); the page follows it.
 */
export function OnboardingReviewPage() {
  const { id } = useParams({ from: '/app/onboarding/$id/review' });
  const qc = useQueryClient();
  const navigate = useNavigate();
  const view = useQuery({
    queryKey: ['onboarding', id],
    queryFn: () => getOnboarding(id),
    refetchInterval: (q) => (q.state.data?.draft.state === 'provisioning' ? 1500 : false),
  });
  const roles = useRoleLabels();
  const debug = useDebugMode();
  const [confirmed, setConfirmed] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [cancelReason, setCancelReason] = useState('');
  const emailCheck = useEmailCheck(view.data?.person.email ?? '');
  const refresh = () => {
    for (const key of [['onboarding', id], ['people'], ['drafts'], ['dashboard'], ['pending-grants']]) void qc.invalidateQueries({ queryKey: key });
  };

  const complete = useMutation({ mutationFn: () => completeOnboarding(id), onSuccess: refresh });
  const cancel = useMutation({
    mutationFn: () => cancelOnboarding(id, cancelReason),
    onSuccess: () => {
      refresh();
      void navigate({ to: '/onboarding' });
    },
  });

  if (view.isLoading) return <p className="p-6 text-muted-foreground">Loading…</p>;
  if (view.error || !view.data) return <p className="p-6 text-destructive">{(view.error as Error)?.message}</p>;
  const { draft, person: p, department, missing_fields, provisioning } = view.data;
  const done = draft.state === 'active';
  const working = draft.state === 'provisioning';
  const result: CompleteResult | null =
    provisioning?.status === 'succeeded' && provisioning.result
      ? provisioning.result
      : provisioning?.status === 'failed' && draft.state === 'draft'
        ? { ok: false, error: provisioning.error, jobs: [], groups_applied: null, account_created: false, password_setup_email_sent: false }
        : null;
  const missing = Object.keys(missing_fields ?? {});
  const ec = p.emergency_contact ?? {};
  const socials = SOCIAL_NETWORKS.filter((n) => p.social_handles?.[n.key]);
  const refs = (draft.roles ?? []).map((r) => r.role_name);
  const byModule = new Map<string, string[]>();
  for (const ref of refs) {
    const l = roles.get(ref);
    byModule.set(l.module, [...(byModule.get(l.module) ?? []), l.label]);
  }
  const groups = [department?.group_name, ...(draft.roles ?? []).map((r) => r.group_name)].filter(Boolean);
  const name = p.nickname || p.first_name;

  return (
    <div className="p-6 max-w-4xl space-y-5">
      <Link to="/onboarding" className="text-sm text-muted-foreground hover:underline">← Onboarding</Link>
      <div>
        <h1 className="text-2xl font-bold">{done ? 'Onboarding completed' : 'Review & complete'}</h1>
        <p className="text-sm text-muted-foreground">
          {done ? `${p.display_name} has an account and is active.` : 'Check everything below. Nothing is created until you press Complete.'}
        </p>
      </div>
      <OnboardingSteps current="review" id={id} locked={done || working} />

      {working && <Working status={provisioning} />}
      {result && !working && <CompleteOutcome result={result} email={p.email} personId={p.id} debug={debug} />}
      {done && !result && (view.data.pending_grants?.length ?? 0) > 0 && (
        <WaitingList grants={view.data.pending_grants!} label={roles.text} />
      )}

      {missing.length > 0 && !done && (
        <div className="rounded-md border border-warning/40 bg-warning-soft p-3 text-sm text-warning-foreground">
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
        <Row label="Login name" value={p.username} />
        <Row label="Date of birth" value={p.date_of_birth} />
        <Row label="Private email" value={p.email} hint="The set-password email goes here" />
        {emailCheck.data?.deliverable === false && (
          <p className="flex items-start gap-2 rounded-md border border-warning/40 bg-warning-soft px-2 py-1.5 text-xs text-warning-foreground">
            <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" /> {undeliverableText(p.email)}
          </p>
        )}
        {p.org_email && <Row label="Organization email" value={p.org_email} />}
        <Row label="Phone" value={formatPhone(p.phone)} />
        <Row label="Address" value={addressLine(p.address)} />
        {socials.map((n) => (
          <Row key={n.key} label={n.label} value={p.social_handles[n.key]} />
        ))}
        <Row label="Emergency contact" value={[ec.name, formatPhone(ec.phone), ec.email].filter(Boolean).join(' · ')} />
        {ec.comments && <Row label="Emergency notes" value={ec.comments} />}
      </Card>

      <Card title="Department" editTo={done ? undefined : 'details'} id={id}>
        <Row label="Department" value={department?.name} />
        {debug && department && <Row label="Group" value={department.group_name} mono />}
      </Card>

      <Card title="Access" editTo={done ? undefined : 'access'} id={id}>
        {refs.length === 0 && <p className="text-sm text-muted-foreground">Only the department&apos;s access.</p>}
        {[...byModule].map(([mod, labels]) => (
          <Row key={mod} label={mod} value={labels.join(', ')} />
        ))}
        {debug && groups.length > 0 && <Row label="Groups" value={groups.join(', ')} mono />}
      </Card>

      {!done && !working && (
        <section className="rounded-lg border border-border bg-card p-4 space-y-3">
          <h2 className="font-semibold">When you press Complete</h2>
          <ol className="list-decimal space-y-1 pl-5 text-sm">
            <li>
              An account is created for {name}. They sign in as <strong>{p.username || '…'}</strong>.
            </li>
            <li>They get access to: {roles.grouped(refs) || 'their department only'}.</li>
            <li>
              {name} gets an email at <strong>{p.email}</strong> with a link to set their password. After that they can
              sign in to the portal and everything they have access to.
            </li>
            <li>They&apos;re marked as Active.</li>
          </ol>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} />
            I checked the details above.
          </label>
          <div className="flex flex-wrap items-center gap-2">
            <button
              onClick={() => complete.mutate()}
              disabled={!confirmed || missing.length > 0 || !department || complete.isPending}
              className="rounded-pill bg-primary px-5 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-60"
            >
              {complete.isPending ? 'Starting…' : 'Complete onboarding'}
            </button>
            <button type="button" onClick={() => setCancelling((c) => !c)}
              className="inline-flex items-center gap-1 rounded-pill border border-border px-4 py-2 text-sm hover:bg-muted">
              <XCircle className="h-4 w-4" /> Cancel onboarding
            </button>
          </div>
          {complete.error && <p role="alert" className="text-sm text-destructive">{(complete.error as Error).message}</p>}
          {cancelling && (
            <form className="space-y-2 rounded-md border border-border p-3 text-sm" onSubmit={(e) => { e.preventDefault(); cancel.mutate(); }}>
              <label className="block">
                <span className="mb-1 block font-medium">Why is this onboarding cancelled? (optional)</span>
                <input value={cancelReason} onChange={(e) => setCancelReason(e.target.value)}
                  className="w-full rounded-md border border-input bg-background px-3 py-2" />
              </label>
              <p className="text-xs text-muted-foreground">Nothing was created yet. {p.display_name} stays in HR as an applicant unless they have another department.</p>
              {cancel.error && <p className="text-destructive">{(cancel.error as Error).message}</p>}
              <button type="submit" disabled={cancel.isPending} className="rounded-pill bg-destructive px-4 py-1.5 font-semibold text-destructive-foreground">
                Cancel onboarding
              </button>
            </form>
          )}
        </section>
      )}
    </div>
  );
}

function WaitingList({ grants, label }: { grants: PendingGrant[]; label: (ref: string) => string }) {
  return (
    <div className="rounded-md border border-info/30 bg-info-soft p-3 text-sm text-info-foreground space-y-1">
      <p className="flex items-center gap-2 font-semibold">
        <GraduationCap className="h-4 w-4" /> Some access waits for training
      </p>
      <ul className="space-y-0.5">
        {grants.map((g) => (
          <li key={g.id}>
            <strong>{label(g.role_key)}</strong>: Waiting for training: {waitingText(g) || '—'}
          </li>
        ))}
      </ul>
      <p className="text-xs">HR adds these automatically once the training is done in Learn.</p>
    </div>
  );
}

function Working({ status }: { status?: ProvisioningStatus }) {
  return (
    <div role="status" className="rounded-md border border-info/30 bg-info-soft p-3 text-sm text-info-foreground">
      <p className="flex items-center gap-2 font-semibold">
        <Loader2 className="h-4 w-4 animate-spin" /> Setting up their account…
      </p>
      {status && status.attempts > 1 && (
        <p className="mt-1">Attempt {status.attempts} of {status.max_attempts}{status.error ? `; the last attempt failed: ${status.error}` : ''}.</p>
      )}
      <p className="mt-1 text-xs">This runs in the background; you can leave this page.</p>
    </div>
  );
}

function CompleteOutcome({ result, email, personId, debug }: { result: CompleteResult; email: string; personId: string; debug: boolean }) {
  const [details, setDetails] = useState(false);
  const roles = useRoleLabels();
  const resend = useMutation({ mutationFn: () => resendPasswordSetup(personId) });
  if (!result.ok) {
    return (
      <div className="rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm">
        <p className="flex items-center gap-2 font-semibold text-destructive">
          <AlertTriangle className="h-4 w-4" /> Could not complete onboarding
        </p>
        <p className="mt-1 text-muted-foreground">Nothing was finalized; the onboarding is still editable. Fix it and try again.</p>
        {result.error && (
          <>
            <button type="button" onClick={() => setDetails((d) => !d)} className="mt-1 text-xs text-primary hover:underline">
              {details ? 'Hide details' : 'Details'}
            </button>
            {details && <pre className="mt-1 whitespace-pre-wrap break-words rounded bg-muted p-2 font-mono text-[11px]">{result.error}</pre>}
          </>
        )}
      </div>
    );
  }
  return (
    <>
      <div className="rounded-md border border-success/40 bg-success-soft p-3 text-sm text-success-foreground space-y-1">
        <p className="flex items-center gap-2 font-semibold">
          <CheckCircle2 className="h-4 w-4" />
          {result.account_created ? 'Account created' : 'Existing account updated'}
          {debug && result.groups_applied?.length ? <span className="font-mono text-xs font-normal">· {result.groups_applied.join(', ')}</span> : null}
        </p>
        {result.password_setup_email_sent ? (
          <p className="flex items-center gap-2">
            <Mail className="h-4 w-4" /> Email with the set-password link sent to <strong>{email}</strong>.
          </p>
        ) : result.password_setup_error ? (
          <div className="space-y-1">
            <p>
              The set-password email could not be sent{debug ? ` (${result.password_setup_error})` : ''}. The sign-in server needs
              working email (SMTP). Once that is fixed, send it again:
            </p>
            <button type="button" onClick={() => resend.mutate()} disabled={resend.isPending || resend.isSuccess}
              className="rounded-pill border border-border bg-background px-3 py-1 text-xs font-semibold">
              {resend.isSuccess ? 'Email sent' : resend.isPending ? 'Sending…' : 'Resend set-password email'}
            </button>
            {resend.error && <p className="text-destructive">{(resend.error as Error).message}</p>}
          </div>
        ) : result.account_created ? (
          <p>No set-password email is sent on this installation: they sign in through the convention&apos;s SSO.</p>
        ) : (
          <p>They already had an account, so no password email was needed.</p>
        )}
      </div>
      {(result.warnings?.length ?? 0) > 0 && (
        <div className="rounded-md border border-warning/40 bg-warning-soft p-3 text-sm text-warning-foreground">
          {result.warnings!.map((w) => <p key={w}>{w}</p>)}
        </div>
      )}
      {(result.pending_grants?.length ?? 0) > 0 && <WaitingList grants={result.pending_grants!} label={roles.text} />}
    </>
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
