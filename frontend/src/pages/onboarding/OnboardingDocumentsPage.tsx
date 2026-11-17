import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from '@tanstack/react-router';

import { ApiError } from '@/api/client';
import { getOnboarding } from '@/api/onboarding';
import { getSettings } from '@/api/settings';
import {
  attachOnboardingDocument,
  detachOnboardingDocument,
  listDocumentTemplates,
  listOnboardingDocuments,
  sendOnboardingDocument,
  unwaiveOnboardingDocument,
  waiveOnboardingDocument,
  type OnboardingDocument,
} from '@/api/signing';
import { OnboardingSteps } from '@/components/OnboardingSteps';

const STATUS_LABEL: Record<OnboardingDocument['status'], string> = {
  pending: 'Pending',
  sent: 'Sent — waiting for signature',
  signed: 'Signed',
  waived: 'Waived',
  stub_blocked: 'Blocked',
};

const cleared = (s: OnboardingDocument['status']) => s === 'signed' || s === 'waived';

/** Step 3: pick forms, email sign links to the volunteer, wait for sign-hr. */
export function OnboardingDocumentsPage() {
  const { id } = useParams({ from: '/app/onboarding/$id/documents' });
  const navigate = useNavigate();
  const qc = useQueryClient();
  const view = useQuery({ queryKey: ['onboarding', id], queryFn: () => getOnboarding(id) });
  const docs = useQuery({ queryKey: ['onboarding-docs', id], queryFn: () => listOnboardingDocuments(id) });
  const library = useQuery({ queryKey: ['document-templates'], queryFn: listDocumentTemplates });
  const settings = useQuery({ queryKey: ['settings'], queryFn: getSettings, staleTime: 60_000 });
  const [banner, setBanner] = useState<string | null>(null);
  const [waiveReason, setWaiveReason] = useState<Record<string, string>>({});
  const [attachId, setAttachId] = useState('');

  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['onboarding-docs', id] });
    void qc.invalidateQueries({ queryKey: ['onboarding', id] });
  };

  const attach = useMutation({
    mutationFn: (templateId: string) => attachOnboardingDocument(id, templateId),
    onSuccess: () => {
      setBanner(null);
      setAttachId('');
      refresh();
    },
    onError: (e: Error) => setBanner(e.message),
  });
  const detach = useMutation({
    mutationFn: (docId: string) => detachOnboardingDocument(id, docId),
    onSuccess: () => {
      setBanner(null);
      refresh();
    },
    onError: (e: Error) => setBanner(e.message),
  });
  const send = useMutation({
    mutationFn: (docId: string) => sendOnboardingDocument(id, docId),
    onSuccess: (res) => {
      setBanner(`Sign link emailed. Copy if needed: ${res.sign_url}`);
      refresh();
    },
    onError: (e: Error) => {
      setBanner(e instanceof ApiError ? e.message : e.message);
      refresh();
    },
  });
  const waive = useMutation({
    mutationFn: ({ docId, reason }: { docId: string; reason: string }) => waiveOnboardingDocument(id, docId, reason),
    onSuccess: () => {
      setBanner(null);
      refresh();
    },
    onError: (e: Error) => setBanner(e.message),
  });
  const unwaive = useMutation({
    mutationFn: (docId: string) => unwaiveOnboardingDocument(id, docId),
    onSuccess: () => {
      setBanner(null);
      refresh();
    },
    onError: (e: Error) => setBanner(e.message),
  });

  if (view.isLoading || docs.isLoading) return <p className="p-6 text-muted-foreground">Loading…</p>;
  if (view.error || !view.data) return <p className="p-6 text-destructive">{(view.error as Error)?.message}</p>;
  if (docs.error) return <p className="p-6 text-destructive">{(docs.error as Error).message}</p>;

  const { draft, person } = view.data;
  const isDraft = draft.state === 'draft';
  const waiting = draft.state === 'pending_approval';
  const canManageDocs = isDraft || waiting;
  const items = docs.data?.items ?? [];
  const allowWaive = !!settings.data?.signing?.allow_stub_skip;
  const blocking = items.filter((d) => !cleared(d.status));
  const attachedTemplateIds = new Set(items.map((d) => d.template_id));
  const attachable = (library.data?.items ?? []).filter(
    (t) => t.current_version_id && !attachedTemplateIds.has(t.id),
  );

  return (
    <div className="p-6 max-w-3xl space-y-5">
      <div>
        <h1 className="text-2xl font-bold">Documents for {person.display_name}</h1>
        <p className="text-sm text-muted-foreground">
          Attach the forms this volunteer needs (for example NDA, GDPR). Send emails a sign link to{' '}
          <strong>{person.email || 'their private email'}</strong>. They sign at sign-hr; you Approve on Review
          after every required form is signed or waived. Check Mailpit locally for the email.
        </p>
      </div>
      <OnboardingSteps current="documents" id={id} locked={!isDraft && !waiting} />

      {banner && (
        <p className="rounded-md border border-border bg-muted/60 px-3 py-2 text-sm break-all">{banner}</p>
      )}

      {waiting && (
        <p className="rounded-md border border-border bg-muted/40 px-3 py-2 text-sm">
          Packet is locked for signatures. Resend from here if needed; Approve happens on Review after you check the
          signed forms.
        </p>
      )}

      {isDraft && (
        <div className="flex flex-wrap items-end gap-2 rounded-lg border border-border bg-card p-3">
          <label className="text-sm flex-1 min-w-[12rem]">
            <span className="block text-xs text-muted-foreground mb-1">Attach a form from the library</span>
            <select
              className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
              value={attachId}
              onChange={(e) => setAttachId(e.target.value)}
            >
              <option value="">Select…</option>
              {attachable.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name}
                  {t.current_version ? ` · v${t.current_version.version}` : ''}
                  {t.required ? ' (required)' : ''}
                </option>
              ))}
            </select>
          </label>
          <button
            type="button"
            disabled={!attachId || attach.isPending}
            onClick={() => attach.mutate(attachId)}
            className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-50"
          >
            Attach
          </button>
          <Link to="/forms" className="text-xs text-primary hover:underline self-center">
            Forms library
          </Link>
        </div>
      )}

      {items.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No forms attached yet. Publish under Forms, attach what this volunteer needs, then Send for signing on Review.
        </p>
      ) : (
        <ul className="space-y-3">
          {items.map((d) => (
            <li key={d.id} className="rounded-lg border border-border bg-card p-4 space-y-2">
              <div className="flex flex-wrap items-start justify-between gap-2">
                <div>
                  <p className="font-semibold">
                    {d.template_name}
                    {d.template_version ? ` · v${d.template_version}` : ''}
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {STATUS_LABEL[d.status]}
                    {d.status_note ? ` · ${d.status_note}` : ''}
                    {d.waived_by ? ` · by ${d.waived_by}` : ''}
                  </p>
                </div>
                {canManageDocs && (
                  <div className="flex flex-wrap gap-2 text-xs">
                    {(d.status === 'pending' || d.status === 'sent' || d.status === 'stub_blocked') && (
                      <button
                        type="button"
                        disabled={send.isPending}
                        onClick={() => send.mutate(d.id)}
                        className="rounded-pill border border-border px-3 py-1 hover:bg-muted"
                      >
                        {d.status === 'sent' ? 'Resend' : 'Send for signing'}
                      </button>
                    )}
                    {isDraft && d.status === 'pending' && (
                      <button
                        type="button"
                        disabled={detach.isPending}
                        onClick={() => detach.mutate(d.id)}
                        className="rounded-pill border border-border px-3 py-1 hover:bg-muted text-destructive"
                      >
                        Detach
                      </button>
                    )}
                    {d.status === 'waived' ? (
                      <button
                        type="button"
                        disabled={unwaive.isPending || waiting}
                        onClick={() => unwaive.mutate(d.id)}
                        className="rounded-pill border border-border px-3 py-1 hover:bg-muted"
                      >
                        Unwaive
                      </button>
                    ) : allowWaive && d.status !== 'signed' ? (
                      <span className="flex flex-wrap items-center gap-1">
                        <input
                          className="rounded-md border border-input bg-background px-2 py-1 text-xs"
                          placeholder="Waive reason"
                          value={waiveReason[d.id] ?? ''}
                          onChange={(e) => setWaiveReason((s) => ({ ...s, [d.id]: e.target.value }))}
                        />
                        <button
                          type="button"
                          disabled={waive.isPending}
                          onClick={() => waive.mutate({ docId: d.id, reason: waiveReason[d.id] ?? '' })}
                          className="rounded-pill border border-border px-3 py-1 hover:bg-muted"
                        >
                          Waive
                        </button>
                      </span>
                    ) : null}
                  </div>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}

      {blocking.length > 0 && canManageDocs && (
        <p className="text-sm text-warning">
          {blocking.length} form{blocking.length === 1 ? '' : 's'} still need a signature before Approve
          {!allowWaive && ' (Settings → Forms → Allow waiving for local testing)'}.
        </p>
      )}
      {blocking.length === 0 && items.length > 0 && canManageDocs && (
        <p className="text-sm text-muted-foreground">All forms are signed or waived — go to Review to Approve.</p>
      )}

      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          onClick={() => void navigate({ to: '/onboarding/$id/review', params: { id } })}
          className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground"
        >
          Continue to review
        </button>
        {isDraft && (
          <Link to="/onboarding/$id/access" params={{ id }} className="rounded-pill border border-border px-4 py-2 text-sm">
            Back to access
          </Link>
        )}
      </div>
    </div>
  );
}
