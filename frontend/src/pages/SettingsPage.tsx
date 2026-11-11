import { useEffect, useState, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ShieldAlert } from 'lucide-react';

import { deleteWorkspaceAccount, listWorkspaceAccounts } from '@/api/admin';
import { ApiError } from '@/api/client';
import { getRetentionCandidates, getSettings, updateSettings, type Settings } from '@/api/settings';
import { DenConnection } from '@/components/DenConnection';
import { TrainingRequirements } from '@/components/TrainingRequirements';
import { renderWithFallback } from '@/lib/naming';
import { noAutofill } from '@/lib/noAutofill';
import { COUNTRIES } from '@/lib/phone';

const EXAMPLE = { first: 'Jane', last: 'Doe', nickname: 'Sparky Paws' };
const inputCls = 'w-full rounded-md border border-input bg-background px-3 py-2';
const numCls = 'w-24 rounded-md border border-input bg-background px-3 py-2';

const comparable = (s?: Settings) => JSON.stringify(s ? { ...s, identity: { ...s.identity, effective: undefined }, managed_by: undefined } : null);

export function SettingsPage() {
  const qc = useQueryClient();
  const settings = useQuery({ queryKey: ['settings'], queryFn: getSettings });
  const [form, setForm] = useState<Settings | null>(null);
  useEffect(() => {
    if (settings.data) setForm(settings.data);
  }, [settings.data]);

  const save = useMutation({
    mutationFn: () => updateSettings({ ...form!, identity: { ...form!.identity, effective: undefined } }),
    onSuccess: (s) => {
      qc.setQueryData(['settings'], s);
      setForm(s);
    },
  });
  if (!form) return <p className="p-6 text-muted-foreground">{settings.error ? (settings.error as Error).message : 'Loading…'}</p>;

  const set = <K extends keyof Settings>(k: K, v: Settings[K]) => setForm((f) => (f ? { ...f, [k]: v } : f));
  const setIn = <S extends 'identity' | 'retention' | 'workspace'>(section: S, k: keyof Settings[S], v: unknown) =>
    setForm((f) => (f ? { ...f, [section]: { ...f[section], [k]: v } } : f));
  const errs = save.error instanceof ApiError ? ((save.error.details as Record<string, string> | undefined) ?? {}) : {};
  const dirty = comparable(form) !== comparable(settings.data);
  const domain = form.org_email_domain || 'your-org.example';
  const managed = settings.data?.managed_by === 'den';
  const effective = settings.data?.identity.effective;
  const retentionByDen = form.retention.managed_by === 'den';
  let lookupHost = '';
  try {
    lookupHost = form.address_lookup_url ? new URL(form.address_lookup_url).host : '';
  } catch {
    lookupHost = '';
  }

  return (
    <div className="p-6 max-w-2xl space-y-6">
      <div>
        <h1 className="text-2xl font-bold">HR settings</h1>
        <p className="text-sm text-muted-foreground">Defaults used across HR.</p>
      </div>

      <DenConnection base="/api/den" />

      <form className="space-y-6" autoComplete="off" onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
        <section className="space-y-4 rounded-lg border border-border bg-card p-4">
          <h2 className="font-semibold">Organization</h2>
          {managed && (
            <p className="rounded-md border border-info/30 bg-info-soft px-3 py-2 text-sm text-info-foreground">
              Managed in Den: change these under Den → Admin → Organisation. HR picks up changes within a minute.
            </p>
          )}
          <fieldset disabled={managed} className="space-y-4 disabled:opacity-80">
            <Setting label="Organization name" error={errs.organization_name} hint="Used in emails HR sends, e.g. the account-deleted notice.">
              <input {...noAutofill} className={inputCls} value={form.organization_name}
                onChange={(e) => set('organization_name', e.target.value)} placeholder="Your organization" />
            </Setting>
            <Setting label="Organization email domain" error={errs.org_email_domain}
              hint="Every organization email uses this domain; in profiles HR only types the part before the @.">
              <div className="flex items-stretch overflow-hidden rounded-md border border-input bg-background">
                <span className="flex items-center border-r border-input bg-muted px-3 text-muted-foreground">name@</span>
                <input {...noAutofill} className="min-w-0 flex-1 bg-transparent px-3 py-2 outline-none" value={form.org_email_domain}
                  onChange={(e) => set('org_email_domain', e.target.value)} placeholder="your-org.example" />
              </div>
            </Setting>
            <Setting label="Organization email name" error={errs.org_email_template}
              hint={`Suggested name before the @. Example: ${renderWithFallback(form.org_email_template, EXAMPLE) || '…'}@${domain}`}>
              <TemplateInput value={form.org_email_template} onChange={(v) => set('org_email_template', v)} />
            </Setting>
            <Setting label="Login name" error={errs.username_template}
              hint={`What people sign in with. Example: ${renderWithFallback(form.username_template, EXAMPLE) || '…'}`}>
              <TemplateInput value={form.username_template} onChange={(v) => set('username_template', v)} />
            </Setting>
            <p className="text-xs text-muted-foreground">
              Templates use <code>{'{first}'}</code>, <code>{'{last}'}</code> and <code>{'{nickname}'}</code>. Names are
              lowercased, accents removed and spaces become dots; if a placeholder is empty, <code>{'{first}.{last}'}</code> is used.
            </p>
          </fieldset>
        </section>

        <section className="space-y-4 rounded-lg border border-border bg-card p-4">
          <h2 className="font-semibold">Forms</h2>
          <Setting label="Default country" error={errs.default_country} hint="Preselected for phone numbers and addresses.">
            <select className={inputCls} value={form.default_country} onChange={(e) => set('default_country', e.target.value)}>
              <option value="">None</option>
              {COUNTRIES.map((c) => <option key={c.code} value={c.code}>{c.name}</option>)}
            </select>
          </Setting>
          <Setting label="Address search" error={errs.address_lookup_url}
            hint="Suggests addresses while typing, using OpenStreetMap data (a Photon server). Leave empty to turn it off; addresses can always be typed by hand.">
            <input {...noAutofill} className={`${inputCls} font-mono`} value={form.address_lookup_url}
              onChange={(e) => set('address_lookup_url', e.target.value)} placeholder="https://photon.komoot.io" />
          </Setting>
          {lookupHost && (
            <p className="flex items-start gap-2 rounded-md border border-warning/40 bg-warning-soft px-3 py-2 text-xs text-warning-foreground">
              <ShieldAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>
                Privacy: what HR users type into an address field is sent to <strong>{lookupHost}</strong>
                {lookupHost === 'photon.komoot.io' && ' (a free public service run by komoot in Germany, fair use only)'}. Names and other
                fields are not sent. For more privacy, run your own Photon server or leave this empty.
              </span>
            </p>
          )}
        </section>

        <section className="space-y-4 rounded-lg border border-border bg-card p-4">
          <h2 className="font-semibold">Notifications</h2>
          <Setting label="HR admin email addresses" error={errs.admin_notification_emails}
            hint="Comma-separated. Get the warnings before HR deletes data (retention, Google Workspace). Empty: everyone in the HR admin group.">
            <input {...noAutofill} className={inputCls} value={form.admin_notification_emails}
              onChange={(e) => set('admin_notification_emails', e.target.value)} placeholder="hr@your-org.example" />
          </Setting>
        </section>

        <section className="space-y-4 rounded-lg border border-border bg-card p-4">
          <h2 className="font-semibold">Sign-in</h2>
          <p className="text-xs text-muted-foreground">
            How staff sign in. Leave a field empty to use the value from Den
            {effective ? ` (now: ${effective.staff_mode || 'own'}${effective.sso_name ? `, ${effective.sso_name}` : ''})` : ''}.
          </p>
          <Setting label="Staff sign-in" error={errs['identity.staff_mode']}
            hint="With the convention's own SSO (brokered or existing), offboarding warns that the SSO account stays active.">
            <select className={inputCls} value={form.identity.staff_mode} onChange={(e) => setIn('identity', 'staff_mode', e.target.value)}>
              <option value="">From Den</option>
              <option value="own">Con Services accounts (own)</option>
              <option value="brokered">Convention SSO through our realm (brokered)</option>
              <option value="existing">Convention realm used directly (existing)</option>
            </select>
          </Setting>
          <div className="grid gap-4 sm:grid-cols-2">
            <Setting label="SSO name" hint="Shown in the offboarding warning.">
              <input {...noAutofill} className={inputCls} value={form.identity.sso_name} onChange={(e) => setIn('identity', 'sso_name', e.target.value)}
                placeholder={effective?.sso_name || 'Example Con SSO'} />
            </Setting>
            <Setting label="SSO admin page" error={errs['identity.sso_url']} hint="Where to disable the SSO account.">
              <input {...noAutofill} className={inputCls} value={form.identity.sso_url} onChange={(e) => setIn('identity', 'sso_url', e.target.value)}
                placeholder={effective?.sso_url || 'https://sso.example.org/admin'} />
            </Setting>
          </div>
          <Setting label="Identity provider admin console" error={errs['identity.admin_console_url']}
            hint="The address of the “Open Keycloak” links (e.g. https://auth-admin.your-org.example/admin/master/console/#/conservices).">
            <input {...noAutofill} className={inputCls} value={form.identity.admin_console_url}
              onChange={(e) => setIn('identity', 'admin_console_url', e.target.value)} placeholder={effective?.admin_console_url} />
          </Setting>
        </section>

        <RetentionSection form={form} errs={errs} byDen={retentionByDen} setIn={(k, v) => setIn('retention', k, v)} />

        <section className="space-y-4 rounded-lg border border-border bg-card p-4">
          <h2 className="font-semibold">Google Workspace</h2>
          {!form.workspace.available && (
            <p className="rounded-md border border-border bg-muted/60 px-3 py-2 text-sm">
              Not available in this version of HR yet: the Google Workspace client is still being added. You can prepare the settings.
            </p>
          )}
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={form.workspace.enabled} onChange={(e) => setIn('workspace', 'enabled', e.target.checked)} />
            Give every onboarded person a Google Workspace account and mirror departments as Google Groups
          </label>
          <div className="grid gap-4 sm:grid-cols-2">
            <Setting label="Workspace domain" error={errs['workspace.domain']} hint={`Empty: the organization email domain (${domain}).`}>
              <input {...noAutofill} className={inputCls} value={form.workspace.domain} onChange={(e) => setIn('workspace', 'domain', e.target.value)} />
            </Setting>
            <Setting label="Admin to act as" error={errs['workspace.admin_email']} hint="A Workspace admin; the service account acts on their behalf.">
              <input {...noAutofill} className={inputCls} value={form.workspace.admin_email} onChange={(e) => setIn('workspace', 'admin_email', e.target.value)} />
            </Setting>
            <Setting label="Organizational unit" error={errs['workspace.org_unit']} hint="Where new accounts go, e.g. /Volunteers.">
              <input {...noAutofill} className={inputCls} value={form.workspace.org_unit} onChange={(e) => setIn('workspace', 'org_unit', e.target.value)} />
            </Setting>
            <div className="flex gap-4">
              <Setting label="Delete after (days)" error={errs['workspace.delete_after_days']} hint="After offboarding.">
                <input type="number" min={1} className={numCls} value={form.workspace.delete_after_days}
                  onChange={(e) => setIn('workspace', 'delete_after_days', Number(e.target.value))} />
              </Setting>
              <Setting label="Remind (days before)" error={errs['workspace.reminder_days']} hint="Email to HR admins.">
                <input type="number" min={0} className={numCls} value={form.workspace.reminder_days}
                  onChange={(e) => setIn('workspace', 'reminder_days', Number(e.target.value))} />
              </Setting>
            </div>
          </div>
          {form.workspace.available && form.workspace.enabled && <SuspendedAccounts />}
        </section>

        {save.error && Object.keys(errs).length === 0 && <p className="text-sm text-destructive">{(save.error as Error).message}</p>}
        {Object.keys(errs).length > 0 && <p className="text-sm text-destructive">Some settings need fixing (marked above).</p>}
        <div className="sticky bottom-0 flex items-center gap-3 bg-background/90 py-2">
          <button type="submit" disabled={save.isPending || !dirty}
            className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-60">
            {save.isPending ? 'Saving…' : 'Save'}
          </button>
          {save.isSuccess && !dirty && <span className="text-sm text-muted-foreground">Saved.</span>}
        </div>
      </form>

      <TrainingRequirements />
    </div>
  );
}

function RetentionSection({ form, errs, byDen, setIn }: {
  form: Settings;
  errs: Record<string, string>;
  byDen: boolean;
  setIn: (k: keyof Settings['retention'], v: unknown) => void;
}) {
  const candidates = useQuery({ queryKey: ['retention-candidates'], queryFn: getRetentionCandidates });
  const r = form.retention;
  return (
    <section className="space-y-4 rounded-lg border border-border bg-card p-4">
      <h2 className="font-semibold">Personal data retention</h2>
      <p className="text-xs text-muted-foreground">
        The defaults are pending legal review; check them against the law that applies to your convention. HR admins are emailed
        before anything is deleted, and nothing is deleted if that email cannot be sent.
      </p>
      {byDen && (
        <p className="rounded-md border border-info/30 bg-info-soft px-3 py-2 text-sm text-info-foreground">
          Set in Den (Admin → Modules → HR). Change them there.
        </p>
      )}
      <fieldset disabled={byDen} className="space-y-3 disabled:opacity-80">
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={r.enabled} onChange={(e) => setIn('enabled', e.target.checked)} />
          Delete personal data automatically
        </label>
        <div className="grid gap-3 sm:grid-cols-2">
          <Setting label="Applicants (days)" error={errs['retention.applicant_days']} hint="Applicants untouched this long are deleted.">
            <input type="number" min={1} max={3650} className={numCls} value={r.applicant_days} onChange={(e) => setIn('applicant_days', Number(e.target.value))} />
          </Setting>
          <Setting label="Paused people (days)" error={errs['retention.paused_days']} hint="People paused this long are offboarded.">
            <input type="number" min={1} max={3650} className={numCls} value={r.paused_days} onChange={(e) => setIn('paused_days', Number(e.target.value))} />
          </Setting>
          <Setting label="Access history after offboarding (days)" error={errs['retention.offboarded_audit_days']} hint="Who granted or removed what.">
            <input type="number" min={1} max={3650} className={numCls} value={r.offboarded_audit_days}
              onChange={(e) => setIn('offboarded_audit_days', Number(e.target.value))} />
          </Setting>
          <Setting label="Warning (days before)" error={errs['retention.warning_days']} hint="Email to HR admins.">
            <input type="number" min={1} max={90} className={numCls} value={r.warning_days} onChange={(e) => setIn('warning_days', Number(e.target.value))} />
          </Setting>
        </div>
      </fieldset>
      {(candidates.data?.items.length ?? 0) > 0 && (
        <div className="text-sm">
          <p className="font-medium">Due within the warning period:</p>
          <ul className="mt-1 space-y-0.5 text-xs">
            {candidates.data!.items.map((c) => (
              <li key={c.person_id}>
                {c.display_name} ({c.status}) · on or after {new Date(c.due_at).toLocaleDateString()}
                {c.warned_at ? ' · admins warned' : ''}
              </li>
            ))}
          </ul>
          <p className="mt-1 text-xs text-muted-foreground">Edit or reactivate someone to keep them.</p>
        </div>
      )}
    </section>
  );
}

function SuspendedAccounts() {
  const qc = useQueryClient();
  const accounts = useQuery({ queryKey: ['workspace-accounts', 'suspended'], queryFn: () => listWorkspaceAccounts('suspended') });
  const del = useMutation({
    mutationFn: deleteWorkspaceAccount,
    onSettled: () => qc.invalidateQueries({ queryKey: ['workspace-accounts'] }),
  });
  const items = accounts.data?.items ?? [];
  if (items.length === 0) return <p className="text-xs text-muted-foreground">No suspended accounts waiting for deletion.</p>;
  return (
    <div className="space-y-1 text-sm">
      <p className="font-medium">Suspended accounts of offboarded people</p>
      {items.map((a) => (
        <div key={a.id} className="flex items-center justify-between gap-2 text-xs">
          <span>
            {a.primary_email} ({a.display_name}) · deleted on {a.delete_after ? new Date(a.delete_after).toLocaleDateString() : '—'}
            {a.last_error && <span className="text-destructive"> · {a.last_error}</span>}
          </span>
          <button type="button" disabled={del.isPending}
            onClick={() => window.confirm(`Delete ${a.primary_email} with its mail and files now?`) && del.mutate(a.id)}
            className="text-destructive hover:underline">Delete now</button>
        </div>
      ))}
      {del.error && <p className="text-xs text-destructive">{(del.error as Error).message}</p>}
    </div>
  );
}

function Setting({ label, hint, error, children }: { label: string; hint: string; error?: string; children: ReactNode }) {
  return (
    <label className="block text-sm">
      <span className="mb-1 block font-medium">{label}</span>
      {children}
      <span className={`mt-1 block text-xs ${error ? 'text-destructive' : 'text-muted-foreground'}`}>{error ?? hint}</span>
    </label>
  );
}

function TemplateInput({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <div className="space-y-1">
      <input {...noAutofill} className={`${inputCls} font-mono`} value={value} onChange={(e) => onChange(e.target.value)} />
      <div className="flex gap-2 text-xs">
        {['{nickname}', '{first}.{last}', '{first}'].map((t) => (
          <button key={t} type="button" onClick={() => onChange(t)}
            className={`rounded-pill border px-2 py-0.5 font-mono ${value === t ? 'border-primary text-primary' : 'border-border text-muted-foreground hover:text-foreground'}`}>
            {t}
          </button>
        ))}
      </div>
    </div>
  );
}
