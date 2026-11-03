import { useState, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';

import { ApiError } from '@/api/client';
import { getSettings } from '@/api/settings';
import { DEFAULT_USERNAME_TEMPLATE, renderWithFallback } from '@/lib/naming';
import {
  PERSON_STATUSES,
  SOCIAL_NETWORKS,
  type Person,
  type PersonInput,
  type PersonStatus,
} from '@/api/people';
import { OrgEmailInput } from '@/components/OrgEmailInput';

type Props = {
  initial?: Person;
  submitLabel: string;
  pending: boolean;
  error: unknown;
  onSubmit: (input: PersonInput) => void;
  onCancel?: () => void;
  /** Rendered above the volunteer sections (the wizard puts the department here). */
  before?: ReactNode;
  /** Wizard pages manage status themselves. */
  hideStatus?: boolean;
  /** Extra condition for enabling submit (e.g. a department is picked). */
  canSubmitExtra?: boolean;
  /** Existing people: status changes go through the lifecycle actions. */
  statusReadOnly?: boolean;
};

const inputCls =
  'w-full rounded-md border border-input bg-background px-3 py-2 text-sm aria-[invalid=true]:border-destructive';

function fromPerson(p?: Person): PersonInput {
  return {
    first_name: p?.first_name ?? '',
    last_name: p?.last_name ?? '',
    nickname: p?.nickname ?? '',
    date_of_birth: p?.date_of_birth ?? '',
    is_banned: p?.is_banned ?? false,
    email: p && !p.email_missing ? p.email : '',
    org_email: p?.org_email ?? '',
    phone: p?.phone ?? '',
    address: p?.address ?? '',
    social_handles: { ...(p?.social_handles ?? {}) },
    emergency_contact: { ...(p?.emergency_contact ?? {}) },
    status: p?.status ?? 'applicant',
    notes: p?.notes ?? '',
  };
}

function fieldErrors(error: unknown): Record<string, string> {
  if (error instanceof ApiError && error.details && typeof error.details === 'object') {
    return error.details as Record<string, string>;
  }
  return {};
}

export function PersonForm({
  initial,
  submitLabel,
  pending,
  error,
  onSubmit,
  onCancel,
  before,
  hideStatus,
  canSubmitExtra = true,
  statusReadOnly,
}: Props) {
  const [v, setV] = useState<PersonInput>(() => fromPerson(initial));
  const errs = fieldErrors(error);
  const settings = useQuery({ queryKey: ['settings'], queryFn: getSettings, staleTime: 60_000 });
  const username = initial || !settings.data
    ? ''
    : renderWithFallback(settings.data.username_template || DEFAULT_USERNAME_TEMPLATE, {
        first: v.first_name, last: v.last_name, nickname: v.nickname ?? '',
      });
  const set = <K extends keyof PersonInput>(k: K, val: PersonInput[K]) => setV((s) => ({ ...s, [k]: val }));
  const setSocial = (k: string, val: string) =>
    setV((s) => ({ ...s, social_handles: { ...s.social_handles, [k]: val } }));
  const setEmergency = (k: keyof NonNullable<PersonInput['emergency_contact']>, val: string) =>
    setV((s) => ({ ...s, emergency_contact: { ...s.emergency_contact, [k]: val } }));

  const ec = v.emergency_contact ?? {};
  const required = [
    v.first_name, v.last_name, v.nickname, v.date_of_birth, v.email, v.phone, v.address, ec.name, ec.phone,
  ];
  const canSubmit = canSubmitExtra && required.every((x) => (x ?? '').trim() !== '');

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit(v);
      }}
      className="space-y-5"
    >
      {before}
      <Section title="Identity" hint="Legal name is used for the identity provider; the nickname is what people see.">
        <Field label="First name" required error={errs.first_name}>
          <input className={inputCls} required value={v.first_name} aria-invalid={!!errs.first_name}
            onChange={(e) => set('first_name', e.target.value)} autoComplete="given-name" />
        </Field>
        <Field label="Last name" required error={errs.last_name}>
          <input className={inputCls} required value={v.last_name} aria-invalid={!!errs.last_name}
            onChange={(e) => set('last_name', e.target.value)} autoComplete="family-name" />
        </Field>
        <Field label="Nickname" required error={errs.nickname}
          hint={username ? `Login name for a new account: ${username}` : 'Shown instead of the legal name where possible'}>
          <input className={inputCls} required value={v.nickname} aria-invalid={!!errs.nickname}
            onChange={(e) => set('nickname', e.target.value)} />
        </Field>
        <Field label="Date of birth" required error={errs.date_of_birth}>
          <input className={inputCls} type="date" required value={v.date_of_birth} aria-invalid={!!errs.date_of_birth}
            max={new Date().toISOString().slice(0, 10)} onChange={(e) => set('date_of_birth', e.target.value)}
            autoComplete="bday" />
        </Field>
        {!hideStatus && (
          <>
            {statusReadOnly ? (
              <Field label="Status" hint="Change it with Pause / Reactivate / Offboard">
                <input className={`${inputCls} bg-muted`} readOnly
                  value={PERSON_STATUSES.find((s) => s.value === v.status)?.label ?? v.status} />
              </Field>
            ) : (
              <Field label="Status" hint={PERSON_STATUSES.find((s) => s.value === v.status)?.hint}>
                <select className={inputCls} value={v.status} onChange={(e) => set('status', e.target.value as PersonStatus)}>
                  {PERSON_STATUSES.map((s) => (
                    <option key={s.value} value={s.value}>{s.label}</option>
                  ))}
                </select>
              </Field>
            )}
            <label className="flex items-center gap-2 self-end pb-2 text-sm">
              <input type="checkbox" checked={!!v.is_banned} onChange={(e) => set('is_banned', e.target.checked)} />
              <span>Banned <span className="text-xs text-muted-foreground">(blocks onboarding)</span></span>
            </label>
          </>
        )}
      </Section>

      <Section title="Contact" hint="Account emails (setting a password, offboarding notices) go to the private email.">
        <Field label="Private email" required error={errs.email}>
          <input className={inputCls} type="email" required value={v.email} aria-invalid={!!errs.email}
            onChange={(e) => set('email', e.target.value)} autoComplete="email" placeholder="name@example.com" />
        </Field>
        <Field label="Organization email" hint="Workspace address; type the name, the domain is added" error={errs.org_email}>
          <OrgEmailInput className={inputCls} value={v.org_email ?? ''} invalid={!!errs.org_email}
            onChange={(val) => set('org_email', val)} firstName={v.first_name} lastName={v.last_name} nickname={v.nickname ?? ''} />
        </Field>
        <Field label="Phone" required error={errs.phone}>
          <input className={inputCls} type="tel" required value={v.phone} aria-invalid={!!errs.phone}
            onChange={(e) => set('phone', e.target.value)} placeholder="+43 660 1234567" autoComplete="tel" />
        </Field>
        <Field label="Address" required wide error={errs.address}>
          <textarea className={inputCls} rows={2} required value={v.address} aria-invalid={!!errs.address}
            onChange={(e) => set('address', e.target.value)} autoComplete="street-address" />
        </Field>
      </Section>

      <Section title="Social handles" hint="Used by teams to reach volunteers during the event. All optional.">
        {SOCIAL_NETWORKS.map((n) => (
          <Field key={n.key} label={n.label} error={errs[`social_handles.${n.key}`]}>
            <input className={inputCls} value={v.social_handles?.[n.key] ?? ''} placeholder={n.placeholder}
              onChange={(e) => setSocial(n.key, e.target.value)} />
          </Field>
        ))}
      </Section>

      <Section title="Emergency contact" hint="Who to call if something happens on shift.">
        <Field label="Name" required error={errs['emergency_contact.name']}>
          <input className={inputCls} required value={v.emergency_contact?.name ?? ''}
            aria-invalid={!!errs['emergency_contact.name']} onChange={(e) => setEmergency('name', e.target.value)} />
        </Field>
        <Field label="Phone" required error={errs['emergency_contact.phone']}>
          <input className={inputCls} type="tel" required value={v.emergency_contact?.phone ?? ''}
            aria-invalid={!!errs['emergency_contact.phone']} onChange={(e) => setEmergency('phone', e.target.value)} />
        </Field>
        <Field label="Email" error={errs['emergency_contact.email']}>
          <input className={inputCls} type="email" value={v.emergency_contact?.email ?? ''}
            aria-invalid={!!errs['emergency_contact.email']} onChange={(e) => setEmergency('email', e.target.value)} />
        </Field>
        <Field label="Comments" hint="Relationship, languages, medical notes they asked us to keep">
          <input className={inputCls} value={v.emergency_contact?.comments ?? ''} onChange={(e) => setEmergency('comments', e.target.value)} />
        </Field>
      </Section>

      <Section title="Internal notes">
        <Field label="Notes" wide hint="Only visible to HR">
          <textarea className={inputCls} rows={3} value={v.notes} onChange={(e) => set('notes', e.target.value)} />
        </Field>
      </Section>

      {error != null && (
        <p className="text-sm text-destructive">
          {(error as Error).message}
          {Object.keys(errs).length > 0 && ' — check the highlighted fields.'}
        </p>
      )}
      <div className="flex gap-2">
        <button type="submit" disabled={pending || !canSubmit}
          className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-60">
          {pending ? 'Saving…' : submitLabel}
        </button>
        {onCancel && (
          <button type="button" onClick={onCancel} className="rounded-pill border border-border px-4 py-2 text-sm">
            Cancel
          </button>
        )}
        <span className="self-center text-xs text-muted-foreground"><span className="text-destructive">*</span> required</span>
      </div>
    </form>
  );
}

function Section({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <fieldset className="rounded-lg border border-border p-4">
      <legend className="px-1 text-sm font-semibold">{title}</legend>
      {hint && <p className="mb-3 text-xs text-muted-foreground">{hint}</p>}
      <div className="grid gap-3 md:grid-cols-2">{children}</div>
    </fieldset>
  );
}

function Field({ label, required, hint, error, wide, children }: {
  label: string;
  required?: boolean;
  hint?: string;
  error?: string;
  wide?: boolean;
  children: ReactNode;
}) {
  return (
    <label className={`block text-sm ${wide ? 'md:col-span-2' : ''}`}>
      <span className="mb-1 block font-medium">
        {label}
        {required && <span className="text-destructive"> *</span>}
      </span>
      {children}
      {error ? (
        <span className="mt-1 block text-xs text-destructive">{error}</span>
      ) : hint ? (
        <span className="mt-1 block text-xs text-muted-foreground">{hint}</span>
      ) : null}
    </label>
  );
}
