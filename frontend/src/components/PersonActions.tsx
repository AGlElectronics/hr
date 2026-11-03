import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { KeyRound, Pause, Play, UserMinus, UserPlus } from 'lucide-react';

import { ApiError } from '@/api/client';
import {
  changePersonAccess,
  getPersonAccess,
  offboardPerson,
  setPersonStatus,
  type AccountState,
  type Membership,
} from '@/api/lifecycle';
import { AccessPicker } from '@/components/AccessPicker';

/** Invalidate everything that shows a person's status or access. */
export function useInvalidatePerson(personId: string) {
  const qc = useQueryClient();
  return () => {
    for (const key of [['person-access', personId], ['person', personId], ['people'], ['dashboard'], ['drafts'], ['idp-groups']]) {
      void qc.invalidateQueries({ queryKey: key });
    }
  };
}

/**
 * What a person holds through HR and the actions that change it: change
 * access per department, pause/reactivate, offboard. Every action syncs
 * Keycloak on the server.
 */
export function PersonActions({ personId }: { personId: string }) {
  const access = useQuery({ queryKey: ['person-access', personId], queryFn: () => getPersonAccess(personId) });
  const invalidate = useInvalidatePerson(personId);
  const [editing, setEditing] = useState<string | null>(null);
  const [offboarding, setOffboarding] = useState(false);
  const [lastWarnings, setLastWarnings] = useState<string[]>([]);

  const status = useMutation({
    mutationFn: (to: 'active' | 'paused') => setPersonStatus(personId, to),
    onSuccess: invalidate,
  });

  if (access.isLoading) return <p className="text-sm text-muted-foreground">Loading access…</p>;
  if (access.error || !access.data) {
    return <p className="text-sm text-destructive">{(access.error as Error)?.message ?? 'Could not load access'}</p>;
  }
  const { person, memberships, onboardings, account } = access.data;
  const inProgress = onboardings.filter((d) => ['draft', 'provisioning', 'pending_approval'].includes(d.state));
  const offboarded = person.status === 'offboarded';
  const canSync = account.state === 'linked' || account.state === 'none';

  return (
    <div className="space-y-4">
      {lastWarnings.length > 0 && (
        <ul className="space-y-1 rounded-md border border-warning/40 bg-warning-soft px-3 py-2 text-sm text-warning-foreground">
          {lastWarnings.map((w) => <li key={w}>{w}</li>)}
        </ul>
      )}
      {!offboarded && account.state === 'missing' && (
        <p className="rounded-md border border-warning/40 bg-warning-soft px-3 py-2 text-sm text-warning-foreground">
          HR has a Keycloak account on file for this person, but it no longer exists in Keycloak (deleted there, or
          created by the old dev setup). Access can&apos;t be changed until they get a new account: offboard and onboard
          them again.
        </p>
      )}
      {!offboarded && account.state === 'unreachable' && (
        <p className="rounded-md border border-warning/40 bg-warning-soft px-3 py-2 text-sm text-warning-foreground">{account.error}</p>
      )}
      {offboarded && (
        <div className="rounded-md border border-border bg-muted/60 px-3 py-2 text-sm">
          <p className="font-medium">
            Offboarded{person.offboarded_at && ` on ${new Date(person.offboarded_at).toLocaleDateString()}`}
          </p>
          {person.offboard_reason && <p className="text-muted-foreground">{person.offboard_reason}</p>}
          <Link to="/onboarding/new" search={{ person: person.id }}
            className="mt-2 inline-flex items-center gap-1 text-primary hover:underline">
            <UserPlus className="h-3.5 w-3.5" /> Onboard again
          </Link>
        </div>
      )}

      {!offboarded && (
        <section className="space-y-2">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-semibold">Departments &amp; access</h3>
            <AccountBadge account={account} />
          </div>
          {memberships.length === 0 && inProgress.length === 0 && (
            <p className="text-sm text-muted-foreground">Not in any department yet.</p>
          )}
          {memberships.map((m) => (
            <MembershipCard key={m.draft_id} personId={personId} m={m} canEdit={canSync}
              editing={editing === m.draft_id}
              onEdit={() => setEditing(editing === m.draft_id ? null : m.draft_id)}
              onSaved={() => {
                setEditing(null);
                invalidate();
              }} />
          ))}
          {inProgress.map((d) => (
            <div key={d.id} className="flex items-center justify-between rounded-md border border-dashed border-border px-3 py-2 text-sm">
              <span>
                {d.title} <span className="text-xs text-muted-foreground">· {d.state.replace('_', ' ')}</span>
              </span>
              {d.department_id ? (
                <Link to="/onboarding/$id/details" params={{ id: d.id }} className="text-primary hover:underline">
                  Continue onboarding →
                </Link>
              ) : (
                <Link to="/drafts/$id" params={{ id: d.id }} className="text-primary hover:underline">Open draft →</Link>
              )}
            </div>
          ))}
          <Link to="/onboarding/new" search={{ person: person.id }}
            className="inline-flex items-center gap-1 text-sm text-primary hover:underline">
            <UserPlus className="h-3.5 w-3.5" />
            {memberships.length > 0 ? 'Add to another department' : 'Start onboarding'}
          </Link>
        </section>
      )}

      {!offboarded && (
        <section className="flex flex-wrap gap-2 border-t border-border pt-3">
          {person.status === 'active' && (
            <button type="button" disabled={status.isPending} onClick={() => status.mutate('paused')}
              className="inline-flex items-center gap-1.5 rounded-pill border border-border px-3 py-1.5 text-sm hover:bg-muted">
              <Pause className="h-3.5 w-3.5" /> Pause
            </button>
          )}
          {person.status === 'paused' && (
            <button type="button" disabled={status.isPending} onClick={() => status.mutate('active')}
              className="inline-flex items-center gap-1.5 rounded-pill border border-border px-3 py-1.5 text-sm hover:bg-muted">
              <Play className="h-3.5 w-3.5" /> Reactivate
            </button>
          )}
          <button type="button" onClick={() => setOffboarding((o) => !o)}
            className="inline-flex items-center gap-1.5 rounded-pill border border-destructive/40 px-3 py-1.5 text-sm text-destructive hover:bg-destructive/10">
            <UserMinus className="h-3.5 w-3.5" /> Offboard
          </button>
          {status.error && <p className="w-full text-sm text-destructive">{(status.error as Error).message}</p>}
          {person.status === 'paused' && (
            <p className="w-full text-xs text-muted-foreground">Paused people keep their login and access.</p>
          )}
        </section>
      )}

      {offboarding && !offboarded && (
        <OffboardForm personId={personId} memberships={memberships} account={account}
          onDone={(warnings) => {
            setOffboarding(false);
            setLastWarnings(warnings);
            invalidate();
          }}
          onCancel={() => setOffboarding(false)} />
      )}
    </div>
  );
}

function AccountBadge({ account }: { account: AccountState }) {
  const label = {
    none: 'No Keycloak account yet',
    linked: account.enabled ? 'Keycloak login active' : 'Keycloak login disabled',
    missing: 'Keycloak account missing',
    unreachable: 'Keycloak unreachable',
  }[account.state];
  const tone = account.state === 'missing' || account.state === 'unreachable' || (account.state === 'linked' && !account.enabled)
    ? 'text-warning'
    : 'text-muted-foreground';
  return (
    <span className={`inline-flex items-center gap-1 text-xs ${tone}`} title={account.username}>
      <KeyRound className="h-3 w-3" /> {label}
    </span>
  );
}

function MembershipCard({ personId, m, canEdit, editing, onEdit, onSaved }: {
  personId: string;
  m: Membership;
  canEdit: boolean;
  editing: boolean;
  onEdit: () => void;
  onSaved: () => void;
}) {
  return (
    <div className="rounded-md border border-border">
      <div className="flex items-start justify-between gap-2 px-3 py-2">
        <div className="min-w-0">
          <p className="text-sm font-medium">{m.department?.name ?? 'Legacy draft'}</p>
          {m.department && <p className="font-mono text-xs text-muted-foreground">{m.department.group_name}</p>}
          <div className="mt-1 flex flex-wrap gap-1">
            {m.roles.length === 0 && <span className="text-xs text-muted-foreground">No module roles</span>}
            {m.roles.map((r) => (
              <span key={r.id ?? r.role_name} className="rounded-pill bg-primary-soft px-2 py-0.5 text-xs text-primary">
                {r.role_name}
              </span>
            ))}
          </div>
        </div>
        {canEdit && (
          <button type="button" onClick={onEdit} className="shrink-0 text-sm text-primary hover:underline">
            {editing ? 'Close' : 'Change access'}
          </button>
        )}
      </div>
      {editing && canEdit && (
        <div className="border-t border-border p-3">
          <AccessPicker
            plan={m.roles}
            editable
            subject="They"
            defaults={m.department?.default_roles.map((r) => r.role_name)}
            saveLabel="Save & sync to Keycloak"
            onSave={async (roles) => {
              await changePersonAccess(personId, m.draft_id, roles);
              onSaved();
            }}
          />
        </div>
      )}
    </div>
  );
}

function OffboardForm({ personId, memberships, account, onDone, onCancel }: {
  personId: string;
  memberships: Membership[];
  account: AccountState;
  onDone: (warnings: string[]) => void;
  onCancel: () => void;
}) {
  const [reason, setReason] = useState('');
  const [keepAccount, setKeepAccount] = useState(false);
  const mutation = useMutation({
    mutationFn: (hrOnly: boolean) => offboardPerson(personId, { reason, keep_account: keepAccount, hr_only: hrOnly }),
    onSuccess: (res) => onDone(res.warnings ?? []),
  });
  const hasAccount = account.state === 'linked';
  const keycloakFailed =
    account.state === 'unreachable' || (mutation.error instanceof ApiError && mutation.error.code === 'upstream_failed');
  const groups = Array.from(new Set(memberships.flatMap((m) => [
    ...(m.department ? [m.department.group_name] : []),
    ...m.roles.map((r) => r.group_name).filter((g): g is string => !!g),
  ])));

  return (
    <form className="space-y-3 rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm"
      onSubmit={(e) => {
        e.preventDefault();
        mutation.mutate(false);
      }}>
      <p className="font-semibold">Offboard this person</p>
      <ul className="list-disc space-y-0.5 pl-5 text-xs text-muted-foreground">
        <li>{groups.length ? <>Removes them from {groups.map((g) => <code key={g} className="mr-1 font-mono">{g}</code>)}</> : 'No HR groups to remove'}</li>
        {hasAccount && <li>{keepAccount ? 'Keeps their Keycloak login enabled' : 'Disables their Keycloak login'}</li>}
        {account.state === 'none' && <li>They never got a Keycloak account, so only HR changes</li>}
        {account.state === 'missing' && <li>Their Keycloak account no longer exists, so only HR changes</li>}
        <li>Keeps their profile and history; you can onboard them again later</li>
      </ul>
      <label className="block">
        <span className="mb-1 block font-medium">Reason <span className="text-destructive">*</span></span>
        <textarea required rows={2} value={reason} onChange={(e) => setReason(e.target.value)}
          className="w-full rounded-md border border-input bg-background px-3 py-2" placeholder="e.g. stepped down after the event" />
      </label>
      {hasAccount && (
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={keepAccount} onChange={(e) => setKeepAccount(e.target.checked)} />
          Keep their login enabled (only remove groups)
        </label>
      )}
      {mutation.error && <p className="text-destructive">{(mutation.error as Error).message}</p>}
      <div className="flex gap-2">
        <button type="submit" disabled={!reason.trim() || mutation.isPending}
          className="rounded-pill bg-destructive px-4 py-1.5 font-semibold text-destructive-foreground disabled:opacity-60">
          {mutation.isPending ? 'Offboarding…' : 'Confirm offboard'}
        </button>
        {keycloakFailed && (
          <button type="button" disabled={!reason.trim() || mutation.isPending} onClick={() => mutation.mutate(true)}
            className="rounded-pill border border-destructive/40 px-4 py-1.5 text-destructive disabled:opacity-60"
            title="Mark them offboarded in HR now; remove their groups and disable them in the Keycloak console yourself">
            Offboard in HR only
          </button>
        )}
        <button type="button" onClick={onCancel} className="rounded-pill border border-border px-4 py-1.5">Cancel</button>
      </div>
    </form>
  );
}
