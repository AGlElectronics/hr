import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { Ban, KeyRound, Layers, Mail, Pause, Play, ShieldCheck, UserMinus, UserPlus } from 'lucide-react';

import { ApiError } from '@/api/client';
import {
  changePersonAccess,
  getPersonAccess,
  offboardPerson,
  resendPasswordSetup,
  setPersonBan,
  setPersonStatus,
  type AccountState,
  type Membership,
  type OffboardResult,
  type StatusResult,
} from '@/api/lifecycle';
import { can, canGlobal, getMe } from '@/api/me';
import { getSettings, type IdentitySettings } from '@/api/settings';
import { AccessPicker } from '@/components/AccessPicker';
import { useDebugMode } from '@/lib/debug';
import { noAutofill } from '@/lib/noAutofill';
import { useRoleLabels } from '@/lib/roles';

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
 * access per department, pause/reactivate, ban, resend the set-password
 * email, offboard. Every action updates their account on the server, and
 * only actions the signed-in HR user may take are shown.
 */
export function PersonActions({ personId, onRemoved }: { personId: string; onRemoved: (res: OffboardResult) => void }) {
  const access = useQuery({ queryKey: ['person-access', personId], queryFn: () => getPersonAccess(personId) });
  const me = useQuery({ queryKey: ['me'], queryFn: getMe });
  const settings = useQuery({ queryKey: ['settings'], queryFn: getSettings });
  const invalidate = useInvalidatePerson(personId);
  const [editing, setEditing] = useState<string | null>(null);
  const [offboarding, setOffboarding] = useState(false);
  const [banning, setBanning] = useState(false);
  const [warnings, setWarnings] = useState<string[]>([]);

  const done = (res: StatusResult) => {
    setWarnings(res.warnings ?? []);
    invalidate();
  };
  const status = useMutation({ mutationFn: (to: 'active' | 'paused') => setPersonStatus(personId, to), onSuccess: done });
  const unban = useMutation({ mutationFn: () => setPersonBan(personId, false, ''), onSuccess: done });
  const resend = useMutation({ mutationFn: () => resendPasswordSetup(personId) });

  if (access.isLoading) return <p className="text-sm text-muted-foreground">Loading access…</p>;
  if (access.error || !access.data) {
    return <p className="text-sm text-destructive">{(access.error as Error)?.message ?? 'Could not load access'}</p>;
  }
  const { person, memberships, onboardings, account } = access.data;
  const self = me.data?.person_id === person.id;
  const inProgress = onboardings.filter((d) => d.state === 'draft' || d.state === 'provisioning');
  const canSync = (account.state === 'linked' || account.state === 'none' || account.state === 'off') && !self;
  const mayChangeAccess = can(me.data, 'access.manage') && !self;
  const mayPause = can(me.data, 'people.pause') && !self;
  const mayOffboard = canGlobal(me.data, 'people.offboard') && !self;
  const mayOnboard = can(me.data, 'onboarding.manage');

  return (
    <div className="space-y-4">
      {self && (
        <p className="rounded-md border border-border bg-muted/60 px-3 py-2 text-sm">
          This is you. You can&apos;t change your own access, status or ban; ask another HR admin.
        </p>
      )}
      {account.state === 'missing' && (
        <p className="rounded-md border border-warning/40 bg-warning-soft px-3 py-2 text-sm text-warning-foreground">
          HR has an account on file for this person, but it no longer exists. Access can&apos;t be changed until they get
          a new one: offboard them and onboard them again.
        </p>
      )}
      {account.state === 'unreachable' && (
        <p className="rounded-md border border-warning/40 bg-warning-soft px-3 py-2 text-sm text-warning-foreground">{account.error}</p>
      )}
      {person.is_banned && (
        <p className="flex items-center gap-2 rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive">
          <Ban className="h-4 w-4" /> Banned: their account is disabled and they can&apos;t be onboarded again.
        </p>
      )}

      <section className="space-y-2">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold">Departments &amp; access</h3>
          <AccountBadge account={account} />
        </div>
        {memberships.length === 0 && inProgress.length === 0 && (
          <p className="text-sm text-muted-foreground">Not in any department yet.</p>
        )}
        {memberships.map((m) => (
          <MembershipCard key={m.draft_id} personId={personId} m={m} canEdit={canSync && mayChangeAccess}
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
              {d.title} <span className="text-xs text-muted-foreground">· {d.state === 'provisioning' ? 'being set up' : 'in progress'}</span>
            </span>
            <Link to="/onboarding/$id/details" params={{ id: d.id }} className="text-primary hover:underline">
              Continue onboarding →
            </Link>
          </div>
        ))}
        {mayOnboard && !self && !person.is_banned && (
          <Link to="/onboarding/new" search={{ person: person.id }} className="inline-flex items-center gap-1 text-sm text-primary hover:underline">
            <UserPlus className="h-3.5 w-3.5" />
            {memberships.length > 0 ? 'Add to another department' : 'Start onboarding'}
          </Link>
        )}
      </section>

      <section className="flex flex-wrap gap-2 border-t border-border pt-3">
        {mayPause && person.status === 'active' && (
          <button type="button" disabled={status.isPending} onClick={() => status.mutate('paused')}
            className="inline-flex items-center gap-1.5 rounded-pill border border-border px-3 py-1.5 text-sm hover:bg-muted">
            <Pause className="h-3.5 w-3.5" /> Pause
          </button>
        )}
        {mayPause && person.status === 'paused' && (
          <button type="button" disabled={status.isPending} onClick={() => status.mutate('active')}
            className="inline-flex items-center gap-1.5 rounded-pill border border-border px-3 py-1.5 text-sm hover:bg-muted">
            <Play className="h-3.5 w-3.5" /> Reactivate
          </button>
        )}
        {mayChangeAccess && account.state === 'linked' && (
          <button type="button" disabled={resend.isPending} onClick={() => resend.mutate()}
            className="inline-flex items-center gap-1.5 rounded-pill border border-border px-3 py-1.5 text-sm hover:bg-muted"
            title="Emails them a new link to set their password (needs email on the sign-in server)">
            <Mail className="h-3.5 w-3.5" /> {resend.isSuccess ? 'Email sent' : 'Resend set-password email'}
          </button>
        )}
        {mayOffboard && !person.is_banned && (
          <button type="button" onClick={() => setBanning((b) => !b)}
            className="inline-flex items-center gap-1.5 rounded-pill border border-destructive/40 px-3 py-1.5 text-sm text-destructive hover:bg-destructive/10">
            <Ban className="h-3.5 w-3.5" /> Ban
          </button>
        )}
        {mayOffboard && person.is_banned && (
          <button type="button" disabled={unban.isPending} onClick={() => unban.mutate()}
            className="inline-flex items-center gap-1.5 rounded-pill border border-border px-3 py-1.5 text-sm hover:bg-muted">
            <ShieldCheck className="h-3.5 w-3.5" /> Lift ban
          </button>
        )}
        {mayOffboard && (
          <button type="button" onClick={() => setOffboarding((o) => !o)}
            className="inline-flex items-center gap-1.5 rounded-pill border border-destructive/40 px-3 py-1.5 text-sm text-destructive hover:bg-destructive/10">
            <UserMinus className="h-3.5 w-3.5" /> Offboard
          </button>
        )}
        {[status.error, unban.error, resend.error].filter(Boolean).map((e) => (
          <p key={(e as Error).message} role="alert" className="w-full text-sm text-destructive">{(e as Error).message}</p>
        ))}
        {warnings.map((w) => <p key={w} className="w-full text-sm text-warning-foreground">{w}</p>)}
        {person.status === 'paused' && (
          <p className="w-full text-xs text-muted-foreground">Paused people can&apos;t sign in; they keep their access for when they come back.</p>
        )}
      </section>

      {banning && (
        <BanForm personId={personId} name={person.display_name}
          onDone={(res) => { setBanning(false); done(res); }} onCancel={() => setBanning(false)} />
      )}
      {offboarding && (
        <OffboardForm personId={personId} name={person.display_name} account={account} identity={settings.data?.identity.effective}
          onDone={(res) => {
            setOffboarding(false);
            invalidate();
            onRemoved(res);
          }}
          onCancel={() => setOffboarding(false)} />
      )}
    </div>
  );
}

function AccountBadge({ account }: { account: AccountState }) {
  const debug = useDebugMode();
  const label = {
    none: 'No account yet',
    linked: account.enabled ? 'Can sign in' : 'Sign-in disabled',
    missing: 'Account missing',
    deleted: 'Account deleted',
    unreachable: 'Accounts unreachable',
    off: 'Records only (no accounts)',
  }[account.state];
  const tone = account.state === 'missing' || account.state === 'unreachable' || (account.state === 'linked' && !account.enabled)
    ? 'text-warning'
    : 'text-muted-foreground';
  return (
    <span className={`inline-flex items-center gap-1 text-xs ${tone}`} title={account.username}>
      <KeyRound className="h-3 w-3" /> {label}
      {debug && account.username && <span className="font-mono">({account.username})</span>}
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
  const debug = useDebugMode();
  const roles = useRoleLabels();
  return (
    <div className="rounded-md border border-border">
      <div className="flex items-start justify-between gap-2 px-3 py-2">
        <div className="min-w-0">
          <p className="text-sm font-medium">{m.department?.name ?? 'No department'}</p>
          {debug && m.department && <p className="font-mono text-xs text-muted-foreground">{m.department.group_name}</p>}
          <div className="mt-1 flex flex-wrap gap-1">
            {m.roles.length === 0 && <span className="text-xs text-muted-foreground">Only the department&apos;s access</span>}
            {m.roles.map((r) => (
              <span key={r.id ?? r.role_name}
                className={`inline-flex items-center gap-1 rounded-pill px-2 py-0.5 text-xs ${r.retired ? 'bg-warning-soft text-warning-foreground' : 'bg-primary-soft text-primary'}`}
                title={[debug ? r.role_name : '', r.bundle_name ? `from access group ${r.bundle_name}` : 'assigned individually'].filter(Boolean).join(' · ')}>
                {r.bundle_name && <Layers className="h-3 w-3" aria-label={`from ${r.bundle_name}`} />}
                {roles.text(r.role_name)}
                {r.bundle_name && <span className="opacity-70">· {r.bundle_name}</span>}
                {r.retired && <span className="font-semibold">· retired</span>}
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
            bundleIds={m.bundle_ids}
            editable
            subject="They"
            defaults={m.department?.default_roles.map((r) => r.role_name)}
            saveLabel="Save access"
            onSave={async (picked, bundleIds) => {
              await changePersonAccess(personId, m.draft_id, picked, bundleIds);
              onSaved();
            }}
          />
        </div>
      )}
    </div>
  );
}

function BanForm({ personId, name, onDone, onCancel }: {
  personId: string;
  name: string;
  onDone: (res: StatusResult) => void;
  onCancel: () => void;
}) {
  const [reason, setReason] = useState('');
  const mutation = useMutation({ mutationFn: () => setPersonBan(personId, true, reason), onSuccess: onDone });
  return (
    <form className="space-y-3 rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm" autoComplete="off"
      onSubmit={(e) => { e.preventDefault(); mutation.mutate(); }}>
      <p className="font-semibold">Ban {name}</p>
      <p className="text-xs text-muted-foreground">Their account is disabled now and they can&apos;t be onboarded again until the ban is lifted.</p>
      <label className="block">
        <span className="mb-1 block font-medium">Reason <span className="text-destructive">*</span></span>
        <textarea {...noAutofill} required rows={2} value={reason} onChange={(e) => setReason(e.target.value)}
          className="w-full rounded-md border border-input bg-background px-3 py-2" />
        <span className="mt-1 block text-xs text-muted-foreground">Kept in the audit log.</span>
      </label>
      {mutation.error && <p className="text-destructive">{(mutation.error as Error).message}</p>}
      <div className="flex gap-2">
        <button type="submit" disabled={!reason.trim() || mutation.isPending}
          className="rounded-pill bg-destructive px-4 py-1.5 font-semibold text-destructive-foreground disabled:opacity-60">Ban</button>
        <button type="button" onClick={onCancel} className="rounded-pill border border-border px-4 py-1.5">Cancel</button>
      </div>
    </form>
  );
}

function OffboardForm({ personId, name, account, identity, onDone, onCancel }: {
  personId: string;
  name: string;
  account: AccountState;
  identity?: IdentitySettings['effective'];
  onDone: (res: OffboardResult) => void;
  onCancel: () => void;
}) {
  const [reason, setReason] = useState('');
  const [shareReason, setShareReason] = useState(false);
  const mutation = useMutation({
    mutationFn: (hrOnly: boolean) => offboardPerson(personId, { reason, share_reason: shareReason, hr_only: hrOnly }),
    onSuccess: onDone,
  });
  const hasAccount = account.state === 'linked';
  const accountFailed =
    account.state === 'unreachable' || (mutation.error instanceof ApiError && mutation.error.code === 'upstream_failed');
  const sso = identity && (identity.staff_mode === 'brokered' || identity.staff_mode === 'existing');
  const ssoName = identity?.sso_name || "your convention's SSO";

  return (
    <form className="space-y-3 rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm" autoComplete="off"
      onSubmit={(e) => {
        e.preventDefault();
        mutation.mutate(false);
      }}>
      <p className="font-semibold">Offboard {name}</p>
      {sso && (
        <div role="note" className="rounded-md border border-warning/40 bg-warning-soft p-2 text-xs text-warning-foreground">
          <p className="font-semibold">Offboarding in HR does not close the SSO account</p>
          <p>
            This installation signs people in through {ssoName}. Offboarding removes their access to Con Services only. Their
            SSO account, and every other tool that uses it, stays active. Disable it in {ssoName} as well
            {identity?.sso_url && <> (<a href={identity.sso_url} target="_blank" rel="noreferrer" className="underline">open</a>)</>}, following
            your convention&apos;s own offboarding process.
          </p>
        </div>
      )}
      <ul className="list-disc space-y-0.5 pl-5 text-xs text-muted-foreground">
        {hasAccount && <li>Deletes their account, so they can no longer sign in to Con Services</li>}
        {hasAccount && <li>Emails their private address that the account was deleted</li>}
        <li>Removes them from HR: profile, contact details and onboarding history</li>
        <li>Keeps the access history (who granted or removed what, when) for the retention period, then deletes it</li>
      </ul>
      <label className="block">
        <span className="mb-1 block font-medium">Reason <span className="text-destructive">*</span></span>
        <textarea {...noAutofill} required rows={2} value={reason} onChange={(e) => setReason(e.target.value)}
          className="w-full rounded-md border border-input bg-background px-3 py-2" placeholder="e.g. stepped down after the event" />
        <span className="mt-1 block text-xs text-muted-foreground">Not stored; only used for the email if you include it.</span>
      </label>
      {hasAccount && (
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={shareReason} onChange={(e) => setShareReason(e.target.checked)} />
          Include the reason in the email
        </label>
      )}
      {mutation.error && <p className="text-destructive">{(mutation.error as Error).message}</p>}
      <div className="flex flex-wrap gap-2">
        <button type="submit" disabled={!reason.trim() || mutation.isPending}
          className="rounded-pill bg-destructive px-4 py-1.5 font-semibold text-destructive-foreground disabled:opacity-60">
          {mutation.isPending ? 'Offboarding…' : 'Offboard and delete'}
        </button>
        {accountFailed && (
          <button type="button" disabled={!reason.trim() || mutation.isPending} onClick={() => mutation.mutate(true)}
            className="rounded-pill border border-destructive/40 px-4 py-1.5 text-destructive disabled:opacity-60"
            title="Remove them from HR now; their account must then be deleted by whoever runs the login system">
            Remove from HR only
          </button>
        )}
        <button type="button" onClick={onCancel} className="rounded-pill border border-border px-4 py-1.5">Cancel</button>
      </div>
    </form>
  );
}
