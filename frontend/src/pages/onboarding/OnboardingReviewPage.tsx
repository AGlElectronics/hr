import { useEffect, useState, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from '@tanstack/react-router';
import { AlertTriangle, CheckCircle2, GraduationCap, Loader2, Mail, Pencil, X, XCircle } from 'lucide-react';

import { resendPasswordSetup } from '@/api/lifecycle';
import {
  cancelOnboarding,
  completeOnboarding,
  getOnboarding,
  submitForSigning,
  type CompleteResult,
  type ProvisioningStatus,
} from '@/api/onboarding';
import { addressLine, SOCIAL_NETWORKS } from '@/api/people';
import { waitingText, type PendingGrant } from '@/api/roleGate';
import {
  fetchOnboardingDocumentPdf,
  listOnboardingDocuments,
  sendOnboardingDocument,
  type OnboardingDocument,
} from '@/api/signing';
import { OnboardingSteps } from '@/components/OnboardingSteps';
import { useDebugMode } from '@/lib/debug';
import { undeliverableText, useEmailCheck } from '@/lib/emailCheck';
import { formatPhone } from '@/lib/phone';
import { useRoleLabels } from '@/lib/roles';
import { ApiError } from '@/api/client';

const DOC_STATUS: Record<OnboardingDocument['status'], string> = {
  pending: 'Not sent',
  sent: 'Waiting for signature',
  signed: 'Signed',
  waived: 'Waived',
  stub_blocked: 'Blocked',
};

const cleared = (s: OnboardingDocument['status']) => s === 'signed' || s === 'waived';

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
 * Final step: check the packet, send forms for signing, wait, then a human
 * Approves after verifying signatures — only then is the IdP account created.
 */
export function OnboardingReviewPage() {
  const { id } = useParams({ from: '/app/onboarding/$id/review' });
  const qc = useQueryClient();
  const navigate = useNavigate();
  const view = useQuery({
    queryKey: ['onboarding', id],
    queryFn: () => getOnboarding(id),
    refetchInterval: (q) => {
      const st = q.state.data?.draft.state;
      if (st === 'provisioning') return 1500;
      if (st === 'pending_approval') return 4000;
      return false;
    },
  });
  const docs = useQuery({
    queryKey: ['onboarding-docs', id],
    queryFn: () => listOnboardingDocuments(id),
    refetchInterval: () => (view.data?.draft.state === 'pending_approval' ? 4000 : false),
  });
  const roles = useRoleLabels();
  const debug = useDebugMode();
  const [confirmed, setConfirmed] = useState(false);
  const [sigChecked, setSigChecked] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [cancelReason, setCancelReason] = useState('');
  const [banner, setBanner] = useState<string | null>(null);
  const [previewDoc, setPreviewDoc] = useState<OnboardingDocument | null>(null);
  const emailCheck = useEmailCheck(view.data?.person.email ?? '');
  const refresh = () => {
    for (const key of [['onboarding', id], ['onboarding-docs', id], ['people'], ['drafts'], ['dashboard'], ['pending-grants']]) {
      void qc.invalidateQueries({ queryKey: key });
    }
  };

  const submit = useMutation({
    mutationFn: () => submitForSigning(id),
    onSuccess: (res) => {
      setBanner(res.message);
      refresh();
    },
  });
  const sendOne = useMutation({
    mutationFn: (docId: string) => sendOnboardingDocument(id, docId),
    onSuccess: (res) => {
      setBanner('Sign link emailed to the volunteer.');
      refresh();
    },
    onError: (e: Error) => setBanner(e instanceof ApiError ? e.message : e.message),
  });
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
  const waitingSigs = draft.state === 'pending_approval';
  const result: CompleteResult | null =
    provisioning?.status === 'succeeded' && provisioning.result
      ? provisioning.result
      : provisioning?.status === 'failed' && (draft.state === 'draft' || draft.state === 'pending_approval')
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
  const docItems = docs.data?.items ?? [];
  const blocking = docItems.filter((d) => !cleared(d.status));
  const allSigned = docItems.length > 0 && blocking.length === 0;
  const canCancel = draft.state === 'draft' || draft.state === 'pending_approval';
  const canSendForms = draft.state === 'draft' || draft.state === 'pending_approval';

  return (
    <div className="p-6 max-w-4xl space-y-5">
      <Link to="/onboarding" className="text-sm text-muted-foreground hover:underline">← Onboarding</Link>
      <div>
        <h1 className="text-2xl font-bold">
          {done ? 'Onboarding completed' : waitingSigs ? 'Waiting for signatures' : 'Review'}
        </h1>
        <p className="text-sm text-muted-foreground">
          {done
            ? `${p.display_name} has an account and is active.`
            : waitingSigs
              ? 'Forms are out for signing. No account is created until you check the signatures and Approve.'
              : 'Check everything below. Sending for signing does not create an account — Approve after signatures does.'}
        </p>
      </div>
      <OnboardingSteps current="review" id={id} locked={done || working || waitingSigs} />
      {banner && <p className="rounded-md border border-border bg-muted/60 px-3 py-2 text-sm break-all">{banner}</p>}
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

      <Card title="Volunteer" editTo={done || waitingSigs ? undefined : 'details'} id={id}>
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
        <Row label="Address" value={addressLine(p.address)} />
        <Row label="Phone" value={formatPhone(p.phone)} />
        {socials.map((n) => (
          <Row key={n.key} label={n.label} value={p.social_handles[n.key]} />
        ))}
        <Row label="Emergency contact" value={[ec.name, formatPhone(ec.phone), ec.email].filter(Boolean).join(' · ')} />
        {ec.comments && <Row label="Emergency notes" value={ec.comments} />}
      </Card>

      <Card title="Department" editTo={done || waitingSigs ? undefined : 'details'} id={id}>
        <Row label="Department" value={department?.name} />
        {debug && department && <Row label="Group" value={department.group_name} mono />}
      </Card>

      <Card title="Access" editTo={done || waitingSigs ? undefined : 'access'} id={id}>
        {refs.length === 0 && <p className="text-sm text-muted-foreground">Only the department&apos;s access.</p>}
        {[...byModule].map(([mod, labels]) => (
          <Row key={mod} label={mod} value={labels.join(', ')} />
        ))}
        {debug && groups.length > 0 && <Row label="Groups" value={groups.join(', ')} mono />}
      </Card>

      <Card title="Forms" editTo={done || waitingSigs ? undefined : 'documents'} id={id}>
        {docs.isLoading && <p className="text-sm text-muted-foreground">Loading forms…</p>}
        {docItems.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No forms attached.{' '}
            {!done && !working && (
              <Link to="/onboarding/$id/documents" params={{ id }} className="text-primary hover:underline">
                Attach on Documents
              </Link>
            )}
          </p>
        ) : (
          <ul className="space-y-2 text-sm">
            {docItems.map((d) => {
              const canSend =
                canSendForms && (d.status === 'pending' || d.status === 'sent' || d.status === 'stub_blocked');
              const canPreview = d.status === 'signed' || d.status === 'sent' || d.status === 'pending';
              return (
                <li key={d.id} className="flex flex-wrap items-center justify-between gap-2">
                  <span>
                    {d.template_name}
                    {d.template_version ? ` · v${d.template_version}` : ''}
                  </span>
                  <span className="flex flex-wrap items-center gap-2">
                    <span className={cleared(d.status) ? 'text-success' : 'text-warning'}>{DOC_STATUS[d.status]}</span>
                    {canPreview && (
                      <button
                        type="button"
                        onClick={() => setPreviewDoc(d)}
                        className="text-primary hover:underline"
                      >
                        {d.status === 'signed' ? 'Open preview' : 'Preview filled form'}
                      </button>
                    )}
                    {canSend && (
                      <button
                        type="button"
                        disabled={sendOne.isPending}
                        onClick={() => sendOne.mutate(d.id)}
                        className="rounded-pill border border-border px-3 py-1 text-xs hover:bg-muted"
                      >
                        {d.status === 'sent' ? 'Resend' : 'Send'}
                      </button>
                    )}
                  </span>
                </li>
              );
            })}
          </ul>
        )}
      </Card>

      {previewDoc && (
        <SignedFormPreview
          draftId={id}
          doc={previewDoc}
          onClose={() => setPreviewDoc(null)}
        />
      )}

      {!done && !working && (
        <section className="rounded-lg border border-border bg-card p-4 space-y-3">
          {draft.state === 'draft' && (
            <>
              <h2 className="font-semibold">Send for signing</h2>
              <ol className="list-decimal space-y-1 pl-5 text-sm">
                <li>Locks this packet (details stay as shown above).</li>
                <li>
                  Emails a sign link to <strong>{p.email || 'the private email'}</strong> for each attached form (Mailpit
                  locally).
                </li>
                <li>No IdP account yet — you Approve after signatures come back and you have checked them.</li>
              </ol>
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} />
                I checked the details above.
              </label>
              <div className="flex flex-wrap items-center gap-2">
                <button
                  type="button"
                  onClick={() => submit.mutate()}
                  disabled={!confirmed || missing.length > 0 || !department || docItems.length === 0 || submit.isPending}
                  className="rounded-pill bg-primary px-5 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-60"
                >
                  {submit.isPending ? 'Sending…' : 'Send for signing'}
                </button>
                {docItems.length === 0 && (
                  <Link to="/onboarding/$id/documents" params={{ id }} className="text-sm text-primary hover:underline">
                    Attach forms first
                  </Link>
                )}
              </div>
              {submit.error && <p role="alert" className="text-sm text-destructive">{(submit.error as Error).message}</p>}
            </>
          )}

          {waitingSigs && !allSigned && (
            <>
              <h2 className="font-semibold">Waiting for signatures</h2>
              <p className="text-sm text-muted-foreground">
                {blocking.length} form{blocking.length === 1 ? '' : 's'} still need a signature. The volunteer uses the
                link in their email (sign-hr). This page refreshes automatically.
              </p>
              <button
                type="button"
                onClick={() => submit.mutate()}
                disabled={submit.isPending}
                className="rounded-pill border border-border px-4 py-2 text-sm hover:bg-muted"
              >
                {submit.isPending ? 'Resending…' : 'Resend sign links'}
              </button>
              {submit.error && <p role="alert" className="text-sm text-destructive">{(submit.error as Error).message}</p>}
            </>
          )}

          {waitingSigs && allSigned && (
            <>
              <h2 className="font-semibold">Approve &amp; create account</h2>
              <p className="text-sm text-muted-foreground">
                All forms are marked signed (or waived). Open each one if needed, confirm a human really signed, then
                Approve. That creates the IdP account and sends the set-password email to {p.email}.
              </p>
              <ol className="list-decimal space-y-1 pl-5 text-sm">
                <li>
                  Account for {name} as <strong>{p.username || '…'}</strong>.
                </li>
                <li>Access: {roles.grouped(refs) || 'department only'}.</li>
                <li>Set-password email to <strong>{p.email}</strong>.</li>
              </ol>
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={sigChecked} onChange={(e) => setSigChecked(e.target.checked)} />
                I checked the signed forms and they look genuine.
              </label>
              <button
                type="button"
                onClick={() => complete.mutate()}
                disabled={!sigChecked || complete.isPending}
                className="rounded-pill bg-primary px-5 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-60"
              >
                {complete.isPending ? 'Starting…' : 'Approve & create account'}
              </button>
              {complete.error && <p role="alert" className="text-sm text-destructive">{(complete.error as Error).message}</p>}
            </>
          )}

          {canCancel && (
            <>
              <button
                type="button"
                onClick={() => setCancelling((c) => !c)}
                className="inline-flex items-center gap-1 rounded-pill border border-border px-4 py-2 text-sm hover:bg-muted"
              >
                <XCircle className="h-4 w-4" /> Cancel onboarding
              </button>
              {cancelling && (
                <form
                  className="space-y-2 rounded-md border border-border p-3 text-sm"
                  onSubmit={(e) => {
                    e.preventDefault();
                    cancel.mutate();
                  }}
                >
                  <label className="block">
                    <span className="mb-1 block font-medium">Why is this onboarding cancelled? (optional)</span>
                    <input
                      value={cancelReason}
                      onChange={(e) => setCancelReason(e.target.value)}
                      className="w-full rounded-md border border-input bg-background px-3 py-2"
                    />
                  </label>
                  <p className="text-xs text-muted-foreground">
                    Nothing was created yet. {p.display_name} stays in HR as an applicant unless they have another
                    department.
                  </p>
                  {cancel.error && <p className="text-destructive">{(cancel.error as Error).message}</p>}
                  <button
                    type="submit"
                    disabled={cancel.isPending}
                    className="rounded-pill bg-destructive px-4 py-1.5 font-semibold text-destructive-foreground"
                  >
                    Cancel onboarding
                  </button>
                </form>
              )}
            </>
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

function Card({ title, editTo, id, children }: { title: string; editTo?: 'details' | 'access' | 'documents'; id: string; children: ReactNode }) {
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

/** In-app filled PDF preview — no download required. */
function SignedFormPreview({
  draftId,
  doc,
  onClose,
}: {
  draftId: string;
  doc: OnboardingDocument;
  onClose: () => void;
}) {
  const [url, setUrl] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const title = `${doc.template_name}${doc.template_version ? ` · v${doc.template_version}` : ''}`;

  useEffect(() => {
    let objectUrl: string | null = null;
    let cancelled = false;
    setLoading(true);
    setError(null);
    void fetchOnboardingDocumentPdf(draftId, doc.id)
      .then((blob) => {
        if (cancelled) return;
        objectUrl = URL.createObjectURL(blob);
        setUrl(objectUrl);
      })
      .catch((e: Error) => {
        if (!cancelled) setError(e.message || 'Could not load the form');
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [draftId, doc.id]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
      role="dialog"
      aria-modal="true"
      aria-label={`Preview ${title}`}
      onClick={onClose}
    >
      <div
        className="flex max-h-[92vh] w-full max-w-5xl flex-col overflow-hidden rounded-lg border border-border bg-card shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="flex items-center justify-between gap-3 border-b border-border px-4 py-3">
          <div className="min-w-0">
            <h2 className="truncate font-semibold">{title}</h2>
            <p className="text-xs text-muted-foreground">
              Filled from the volunteer&apos;s HR profile — open to check before Approve.
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded-pill p-2 hover:bg-muted"
            aria-label="Close preview"
          >
            <X className="h-4 w-4" />
          </button>
        </header>
        <div className="min-h-0 flex-1 bg-muted">
          {loading && (
            <p className="flex items-center justify-center gap-2 p-10 text-sm text-muted-foreground">
              <Loader2 className="h-4 w-4 animate-spin" /> Loading form…
            </p>
          )}
          {error && <p className="p-6 text-sm text-destructive">{error}</p>}
          {url && !loading && !error && (
            <iframe title={title} src={`${url}#view=FitH`} className="h-[min(78vh,52rem)] w-full bg-background" />
          )}
        </div>
      </div>
    </div>
  );
}
