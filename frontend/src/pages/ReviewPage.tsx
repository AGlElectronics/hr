import { useState } from 'react';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { AlertTriangle, Check, GitMerge, RefreshCw, X } from 'lucide-react';

import { acceptReview, listBundles, listReview, mergeReview, rejectReview, scanReview, type ReviewItem } from '@/api/admin';
import { listDepartments } from '@/api/departments';
import { can, canGlobal, getMe } from '@/api/me';
import { addressLine, listPeople, PAGE_SIZE, type PersonInput } from '@/api/people';
import { SearchInput } from '@/components/SearchInput';
import { noAutofill } from '@/lib/noAutofill';

const SOURCES: Record<ReviewItem['source'], string> = {
  idp_scan: 'Account without HR record',
  self_service: 'Registered themselves',
  import: 'Imported',
};
const STATUSES = [
  { value: 'open', label: 'Open' },
  { value: 'pending', label: 'Missing details' },
  { value: 'ready', label: 'Ready' },
  { value: 'accepted', label: 'Accepted' },
  { value: 'merged', label: 'Merged' },
  { value: 'rejected', label: 'Rejected' },
];

/**
 * People who have an account (or registered, or were imported) but no HR
 * record yet. A human always decides: accept as a new person, merge into an
 * existing one, or reject (which disables their account).
 */
export function ReviewPage() {
  const qc = useQueryClient();
  const me = useQuery({ queryKey: ['me'], queryFn: getMe });
  const [status, setStatus] = useState('open');
  const [q, setQ] = useState('');
  const [offset, setOffset] = useState(0);
  const [open, setOpen] = useState<string | null>(null);
  const list = useQuery({
    queryKey: ['review', status, q, offset],
    queryFn: () => listReview({ status: status === 'open' ? undefined : status, q: q || undefined, limit: PAGE_SIZE, offset }),
    placeholderData: keepPreviousData,
  });
  const scan = useMutation({ mutationFn: scanReview, onSuccess: () => void qc.invalidateQueries({ queryKey: ['review'] }) });
  const items = list.data?.items ?? [];
  const total = list.data?.total ?? 0;

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold">Review queue</h1>
          <p className="text-sm text-muted-foreground">
            Accounts without an HR record, self-registrations and imported rows. Nothing is accepted automatically.
          </p>
        </div>
        {canGlobal(me.data, 'review.manage') && (
          <button type="button" disabled={scan.isPending} onClick={() => scan.mutate()}
            className="inline-flex shrink-0 items-center gap-1 rounded-pill border border-border px-4 py-2 text-sm hover:bg-muted"
            title="Looks for sign-in accounts that have no HR record yet">
            <RefreshCw className={`h-4 w-4 ${scan.isPending ? 'animate-spin' : ''}`} /> Check accounts now
          </button>
        )}
      </div>
      {scan.data && (
        <p className="text-sm text-muted-foreground">
          Checked {scan.data.seen} accounts: {scan.data.created} new in the queue, {scan.data.linked} linked by verified email.
        </p>
      )}
      {scan.error && <p role="alert" className="text-sm text-destructive">{(scan.error as Error).message}</p>}

      <section className="rounded-lg border border-border bg-card">
        <header className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
          <select value={status} onChange={(e) => { setStatus(e.target.value); setOffset(0); }} aria-label="Status"
            className="rounded-md border border-input bg-background px-3 py-1.5 text-sm">
            {STATUSES.map((s) => <option key={s.value} value={s.value}>{s.label}</option>)}
          </select>
          <SearchInput value={q} onChange={(v) => { setQ(v); setOffset(0); }} placeholder="Name or email…" className="ml-auto w-64" />
        </header>
        {list.isLoading && <p className="px-4 py-3 text-muted-foreground">Loading…</p>}
        {list.error && <p className="px-4 py-3 text-destructive">{(list.error as Error).message}</p>}
        <ul className="divide-y divide-border">
          {items.map((it) => (
            <li key={it.id}>
              <button type="button" onClick={() => setOpen(open === it.id ? null : it.id)}
                className={`flex w-full items-center gap-3 px-4 py-3 text-left hover:bg-muted/40 ${open === it.id ? 'bg-primary-soft/50' : ''}`}>
                <span className="flex-1 min-w-0">
                  <span className="block font-medium">{[it.first_name, it.last_name].filter(Boolean).join(' ') || it.email}</span>
                  <span className="block text-xs text-muted-foreground">
                    {it.email}{it.email_verified ? ' (verified)' : ''} · {SOURCES[it.source]}
                  </span>
                </span>
                {it.match_person_id && (
                  <span className="rounded-pill bg-warning-soft px-2 py-0.5 text-xs text-warning-foreground">
                    {it.match_kind === 'verified_email' ? 'Matches a person' : 'Possible match'}
                  </span>
                )}
                <StatusPill status={it.status} />
              </button>
              {open === it.id && <ReviewDetail item={it} canEdit={can(me.data, 'review.manage')} onDone={() => setOpen(null)} />}
            </li>
          ))}
          {!list.isLoading && items.length === 0 && <li className="px-4 py-6 text-sm text-muted-foreground">Nothing to review.</li>}
        </ul>
        {total > PAGE_SIZE && (
          <footer className="flex items-center justify-between border-t border-border px-4 py-2 text-sm">
            <span className="text-muted-foreground">{offset + 1}–{Math.min(offset + PAGE_SIZE, total)} of {total}</span>
            <span className="flex gap-2">
              <button type="button" disabled={offset === 0} onClick={() => setOffset((o) => Math.max(0, o - PAGE_SIZE))}
                className="rounded-pill border border-border px-3 py-1 disabled:opacity-50">Previous</button>
              <button type="button" disabled={offset + PAGE_SIZE >= total} onClick={() => setOffset((o) => o + PAGE_SIZE)}
                className="rounded-pill border border-border px-3 py-1 disabled:opacity-50">Next</button>
            </span>
          </footer>
        )}
      </section>
    </div>
  );
}

function StatusPill({ status }: { status: ReviewItem['status'] }) {
  const label = STATUSES.find((s) => s.value === status)?.label ?? status;
  const tone = status === 'ready' ? 'bg-primary-soft text-primary' : status === 'rejected' ? 'bg-destructive/10 text-destructive' : 'bg-muted text-muted-foreground';
  return <span className={`rounded-pill px-2 py-0.5 text-xs font-semibold ${tone}`}>{label}</span>;
}

function personFrom(it: ReviewItem): PersonInput {
  return {
    first_name: it.first_name,
    last_name: it.last_name,
    nickname: it.nickname || undefined,
    email: it.email,
    phone: it.phone || undefined,
    date_of_birth: it.data.date_of_birth || undefined,
    address: it.data.address,
    emergency_contact: it.data.emergency_contact,
    social_handles: it.data.social_handles,
    org_email: it.data.org_email || undefined,
  };
}

function ReviewDetail({ item, canEdit, onDone }: { item: ReviewItem; canEdit: boolean; onDone: () => void }) {
  const qc = useQueryClient();
  const me = useQuery({ queryKey: ['me'], queryFn: getMe });
  const depts = useQuery({ queryKey: ['departments'], queryFn: listDepartments });
  const bundles = useQuery({ queryKey: ['access-bundles'], queryFn: listBundles });
  const [mode, setMode] = useState<'accept' | 'merge' | 'reject' | null>(null);
  const [departmentId, setDepartmentId] = useState(item.department_id ?? '');
  const [bundleIds, setBundleIds] = useState<string[]>(item.bundle_ids ?? []);
  const [complete, setComplete] = useState(false);
  const [reason, setReason] = useState('');
  const [mergeInto, setMergeInto] = useState(item.match_person_id ?? '');
  const [personQ, setPersonQ] = useState('');
  const people = useQuery({
    queryKey: ['people', 'merge-search', personQ],
    queryFn: () => listPeople({ q: personQ, limit: 10 }),
    enabled: mode === 'merge' && personQ.length > 1,
  });
  const decided = () => {
    void qc.invalidateQueries({ queryKey: ['review'] });
    void qc.invalidateQueries({ queryKey: ['people'] });
  };
  const accept = useMutation({
    mutationFn: () => acceptReview(item.id, { person: personFrom(item), department_id: departmentId, bundle_ids: bundleIds, complete }),
    onSuccess: decided,
  });
  const merge = useMutation({ mutationFn: () => mergeReview(item.id, mergeInto), onSuccess: () => { decided(); onDone(); } });
  const reject = useMutation({ mutationFn: () => rejectReview(item.id, reason), onSuccess: decided });
  const leadOf = new Set(me.data?.lead_of ?? []);
  const deptChoices = (depts.data?.items ?? []).filter((d) => canGlobal(me.data, 'review.manage') || leadOf.has(d.id));
  const open = item.status === 'pending' || item.status === 'ready';
  const d = item.data;

  return (
    <div className="space-y-3 border-t border-border bg-muted/20 px-4 py-3 text-sm">
      <dl className="grid gap-x-4 gap-y-1 sm:grid-cols-2">
        <Field label="Nickname" value={item.nickname} />
        <Field label="Phone" value={item.phone} />
        <Field label="Date of birth" value={d.date_of_birth} />
        <Field label="Address" value={addressLine(d.address)} />
        <Field label="Emergency contact" value={d.emergency_contact?.name} />
        <Field label="Department" value={depts.data?.items.find((x) => x.id === item.department_id)?.name} />
      </dl>
      {(d.warnings ?? []).map((w) => (
        <p key={w} className="flex items-center gap-1 text-xs text-warning-foreground"><AlertTriangle className="h-3 w-3" /> {w}</p>
      ))}
      {item.match_person_id && (
        <p className="text-xs">
          {item.match_kind === 'verified_email'
            ? 'An HR person has this email, and the sign-in server verified it. '
            : 'An HR person has this email, but it is not verified: check before merging. '}
          <Link to="/people/$id" params={{ id: item.match_person_id }} className="text-primary hover:underline">Open person</Link>
        </p>
      )}
      {item.status === 'pending' && <p className="text-xs text-muted-foreground">Some required details are missing; complete them on the person page after accepting.</p>}
      {item.decided_by && (
        <p className="text-xs text-muted-foreground">
          {item.status} by {item.decided_by}{item.decided_at ? ` on ${new Date(item.decided_at).toLocaleString()}` : ''}
          {item.decision_note ? `: ${item.decision_note}` : ''}
        </p>
      )}
      {item.person_id && (
        <Link to="/people/$id" params={{ id: item.person_id }} className="text-xs text-primary hover:underline">Open their HR record →</Link>
      )}

      {open && canEdit && (
        <div className="flex flex-wrap gap-2">
          <button type="button" onClick={() => setMode('accept')}
            className="inline-flex items-center gap-1 rounded-pill bg-primary px-3 py-1.5 text-primary-foreground"><Check className="h-3.5 w-3.5" /> Accept</button>
          <button type="button" onClick={() => setMode('merge')}
            className="inline-flex items-center gap-1 rounded-pill border border-border px-3 py-1.5 hover:bg-muted"><GitMerge className="h-3.5 w-3.5" /> Merge</button>
          <button type="button" onClick={() => setMode('reject')}
            className="inline-flex items-center gap-1 rounded-pill border border-destructive/40 px-3 py-1.5 text-destructive hover:bg-destructive/10"><X className="h-3.5 w-3.5" /> Reject</button>
        </div>
      )}

      {mode === 'accept' && open && (
        <form className="space-y-2 rounded-md border border-border bg-card p-3" onSubmit={(e) => { e.preventDefault(); accept.mutate(); }}>
          <label className="block">
            <span className="mb-1 block font-medium">Department</span>
            <select required value={departmentId} onChange={(e) => setDepartmentId(e.target.value)}
              className="w-full rounded-md border border-input bg-background px-3 py-2">
              <option value="">Choose…</option>
              {deptChoices.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
            </select>
          </label>
          {(bundles.data?.items.length ?? 0) > 0 && (
            <fieldset>
              <legend className="mb-1 font-medium">Access groups</legend>
              {bundles.data!.items.map((b) => (
                <label key={b.id} className="mr-4 inline-flex items-center gap-1">
                  <input type="checkbox" checked={bundleIds.includes(b.id)}
                    onChange={(e) => setBundleIds((ids) => (e.target.checked ? [...ids, b.id] : ids.filter((x) => x !== b.id)))} />
                  {b.name}
                </label>
              ))}
            </fieldset>
          )}
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={complete} onChange={(e) => setComplete(e.target.checked)} disabled={!can(me.data, 'access.manage')} />
            Give access right away (otherwise the onboarding stays open for review)
          </label>
          {accept.error && <p role="alert" className="text-destructive">{(accept.error as Error).message}</p>}
          {accept.data && (
            <p className="text-primary">
              Accepted.{' '}
              <Link to="/onboarding/$id/review" params={{ id: accept.data.onboarding.draft.id }} className="underline">Open the onboarding</Link>
            </p>
          )}
          <button type="submit" disabled={!departmentId || accept.isPending || !!accept.data}
            className="rounded-pill bg-primary px-4 py-1.5 font-semibold text-primary-foreground disabled:opacity-60">
            {accept.isPending ? 'Accepting…' : 'Accept as new person'}
          </button>
        </form>
      )}

      {mode === 'merge' && open && (
        <form className="space-y-2 rounded-md border border-border bg-card p-3" onSubmit={(e) => { e.preventDefault(); merge.mutate(); }}>
          <p className="text-xs text-muted-foreground">Links this account to an existing HR person. Their HR record is kept as it is.</p>
          <SearchInput value={personQ} onChange={setPersonQ} placeholder="Find the person…" className="w-64" />
          <ul className="space-y-1">
            {(people.data?.items ?? []).map((p) => (
              <li key={p.id}>
                <label className="inline-flex items-center gap-2">
                  <input type="radio" name={`merge-${item.id}`} checked={mergeInto === p.id} onChange={() => setMergeInto(p.id)} />
                  {p.display_name} <span className="text-xs text-muted-foreground">{p.email}</span>
                </label>
              </li>
            ))}
          </ul>
          {mergeInto && <p className="text-xs">Merging into person <span className="font-mono">{mergeInto.slice(0, 8)}</span></p>}
          {merge.error && <p role="alert" className="text-destructive">{(merge.error as Error).message}</p>}
          <button type="submit" disabled={!mergeInto || merge.isPending}
            className="rounded-pill bg-primary px-4 py-1.5 font-semibold text-primary-foreground disabled:opacity-60">Merge</button>
        </form>
      )}

      {mode === 'reject' && open && (
        <form className="space-y-2 rounded-md border border-destructive/40 bg-destructive/5 p-3" autoComplete="off"
          onSubmit={(e) => { e.preventDefault(); reject.mutate(); }}>
          <p className="text-xs">
            {item.idp_user_id ? 'Their sign-in account is disabled. ' : ''}The entry stays in the queue as rejected.
          </p>
          <textarea {...noAutofill} rows={2} value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Reason (kept in the audit log)"
            className="w-full rounded-md border border-input bg-background px-3 py-2" />
          {reject.error && <p role="alert" className="text-destructive">{(reject.error as Error).message}</p>}
          {reject.data?.warnings.map((w) => <p key={w} className="text-warning-foreground">{w}</p>)}
          <button type="submit" disabled={reject.isPending}
            className="rounded-pill bg-destructive px-4 py-1.5 font-semibold text-destructive-foreground disabled:opacity-60">Reject</button>
        </form>
      )}
    </div>
  );
}

function Field({ label, value }: { label: string; value?: string }) {
  return (
    <div className="flex gap-2">
      <dt className="w-36 shrink-0 text-muted-foreground">{label}</dt>
      <dd>{value || '—'}</dd>
    </div>
  );
}
