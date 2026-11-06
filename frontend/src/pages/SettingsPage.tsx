import { useEffect, useState, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { ApiError } from '@/api/client';
import { getSettings, updateSettings, type Settings } from '@/api/settings';
import { DenConnection } from '@/components/DenConnection';
import { TrainingRequirements } from '@/components/TrainingRequirements';
import { renderWithFallback } from '@/lib/naming';
import { noAutofill } from '@/lib/noAutofill';
import { COUNTRIES } from '@/lib/phone';

const EMPTY: Omit<Settings, 'managed_by'> = {
  organization_name: '',
  org_email_domain: '',
  username_template: '',
  org_email_template: '',
  default_country: '',
  address_lookup_url: '',
};
const EXAMPLE = { first: 'Jane', last: 'Doe', nickname: 'Sparky Paws' };
const inputCls = 'w-full rounded-md border border-input bg-background px-3 py-2';

export function SettingsPage() {
  const qc = useQueryClient();
  const settings = useQuery({ queryKey: ['settings'], queryFn: getSettings });
  const [form, setForm] = useState<Settings>(EMPTY);
  useEffect(() => {
    if (settings.data) setForm(settings.data);
  }, [settings.data]);
  const set = (k: keyof Settings, v: string) => setForm((f) => ({ ...f, [k]: v }));

  const save = useMutation({
    mutationFn: () => updateSettings(form),
    onSuccess: (s) => {
      qc.setQueryData(['settings'], s);
      setForm(s);
    },
  });
  const errs =
    save.error instanceof ApiError ? ((save.error.details as Record<string, string> | undefined) ?? {}) : {};
  const dirty = (Object.keys(EMPTY) as (keyof Settings)[]).some((k) => form[k] !== (settings.data?.[k] ?? ''));
  const domain = form.org_email_domain || 'your-org.example';
  const managed = settings.data?.managed_by === 'den';

  return (
    <div className="p-6 max-w-2xl space-y-6">
      <div>
        <h1 className="text-2xl font-bold">HR settings</h1>
        <p className="text-sm text-muted-foreground">Defaults used across HR.</p>
      </div>

      <DenConnection base="/api/den" />

      <form className="space-y-6" autoComplete="off"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}>
        <section className="space-y-4 rounded-lg border border-border bg-card p-4">
          <h2 className="font-semibold">Organization</h2>
          {managed && (
            <p className="rounded-md border border-info/30 bg-info-soft px-3 py-2 text-sm text-info-foreground">
              Managed in Den: change these under Den → Admin → Organisation. HR picks up changes within a minute.
            </p>
          )}
          <fieldset disabled={managed} className="space-y-4 disabled:opacity-80">
            <Setting label="Organization name" error={errs.organization_name}
              hint="Used in emails HR sends, e.g. the account-deleted notice.">
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
          <Setting label="Default country" error={errs.default_country}
            hint="Preselected for phone numbers and addresses.">
            <select className={inputCls} value={form.default_country} onChange={(e) => set('default_country', e.target.value)}>
              <option value="">None</option>
              {COUNTRIES.map((c) => <option key={c.code} value={c.code}>{c.name}</option>)}
            </select>
          </Setting>
          <Setting label="Address search" error={errs.address_lookup_url}
            hint="Suggests addresses while typing, using OpenStreetMap data. Leave empty to turn it off; addresses can always be typed by hand.">
            <input {...noAutofill} className={`${inputCls} font-mono`} value={form.address_lookup_url}
              onChange={(e) => set('address_lookup_url', e.target.value)} placeholder="https://photon.komoot.io" />
          </Setting>
        </section>

        {save.error && Object.keys(errs).length === 0 && <p className="text-sm text-destructive">{(save.error as Error).message}</p>}
        <div className="flex items-center gap-3">
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
