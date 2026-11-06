import { useEffect, useState, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';

import { ApiError } from '@/api/client';
import {
  checkUsername,
  EMPTY_ADDRESS,
  PERSON_STATUSES,
  SOCIAL_NETWORKS,
  type Person,
  type PersonInput,
  type PersonStatus,
} from '@/api/people';
import { getSettings } from '@/api/settings';
import { AddressFields } from '@/components/AddressFields';
import { EmailInput } from '@/components/EmailInput';
import { OrgEmailInput } from '@/components/OrgEmailInput';
import { PhoneInput } from '@/components/PhoneInput';
import { useEmailCheck } from '@/lib/emailCheck';
import { renderWithFallback } from '@/lib/naming';
import { noAutofill } from '@/lib/noAutofill';
import { phoneError } from '@/lib/phone';
import { emailError, nameError } from '@/lib/validation';

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
    email: p?.email ?? '',
    org_email: p?.org_email ?? '',
    phone: p?.phone ?? '',
    address: { ...EMPTY_ADDRESS, ...(p?.address ?? {}) },
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

function useDebounced<T>(v: T, ms: number): T {
  const [d, setD] = useState(v);
  useEffect(() => {
    const t = setTimeout(() => setD(v), ms);
    return () => clearTimeout(t);
  }, [v, ms]);
  return d;
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
  const [emailOk, setEmailOk] = useState(false);
  const [orgKept, setOrgKept] = useState('');
  const serverErrs = fieldErrors(error);
  const settings = useQuery({ queryKey: ['settings'], queryFn: getSettings, staleTime: 60_000 });
  const set = <K extends keyof PersonInput>(k: K, val: PersonInput[K]) => setV((s) => ({ ...s, [k]: val }));
  const setSocial = (k: string, val: string) =>
    setV((s) => ({ ...s, social_handles: { ...s.social_handles, [k]: val } }));
  const setEmergency = (k: keyof NonNullable<PersonInput['emergency_contact']>, val: string) =>
    setV((s) => ({ ...s, emergency_contact: { ...s.emergency_contact, [k]: val } }));
  const ec = v.emergency_contact ?? {};

  const names = useDebounced({ first: v.first_name.trim(), last: v.last_name.trim(), nickname: (v.nickname ?? '').trim() }, 400);
  const login = useQuery({
    queryKey: ['username-check', names, initial?.id],
    queryFn: () => checkUsername({ ...names, person_id: initial?.id }),
    enabled: !!(names.nickname || (names.first && names.last)) && !nameError(names.nickname) && !nameError(names.first) && !nameError(names.last),
    staleTime: 30_000,
    retry: false,
  });
  const loginTaken = login.data && !login.data.available;
  const loginRenamed = !!initial?.username && !!login.data?.username && login.data.username !== initial.username;

  const emailCheck = useEmailCheck(v.email);
  const undeliverable = emailCheck.data?.deliverable === false;

  // Organization email follow-up when a name change alters the suggested address.
  const domain = settings.data?.org_email_domain ?? '';
  const tpl = settings.data?.org_email_template;
  const oldSug = initial ? renderWithFallback(tpl, { first: initial.first_name, last: initial.last_name, nickname: initial.nickname ?? '' }) : '';
  const newSug = renderWithFallback(tpl, { first: v.first_name, last: v.last_name, nickname: v.nickname ?? '' });
  const initialOrg = initial?.org_email ?? '';
  const orgUnchanged = !!initialOrg && v.org_email === initialOrg && !!domain && !!newSug && newSug !== oldSug;
  const orgFollows = orgUnchanged && initialOrg.toLowerCase() === `${oldSug}@${domain}`.toLowerCase();
  const orgAsk = orgUnchanged && !orgFollows && orgKept !== newSug;
  const newOrg = `${newSug}@${domain}`;

  const errs: Record<string, string | undefined> = {
    ...serverErrs,
    first_name: nameError(v.first_name) ?? serverErrs.first_name,
    last_name: nameError(v.last_name) ?? serverErrs.last_name,
    nickname: nameError(v.nickname) ?? (loginTaken ? login.data?.reason || 'That nickname is already taken' : undefined) ?? serverErrs.nickname,
    email: emailError(v.email) ?? serverErrs.email,
    phone: phoneError(v.phone) ?? serverErrs.phone,
    'emergency_contact.name': nameError(ec.name) ?? serverErrs['emergency_contact.name'],
    'emergency_contact.phone': phoneError(ec.phone) ?? serverErrs['emergency_contact.phone'],
    'emergency_contact.email': emailError(ec.email) ?? serverErrs['emergency_contact.email'],
  };
  const clientInvalid = [
    nameError(v.first_name), nameError(v.last_name), nameError(v.nickname), nameError(ec.name),
    emailError(v.email), emailError(ec.email), phoneError(v.phone), phoneError(ec.phone),
  ].some(Boolean);

  const a = v.address;
  const required = [
    v.first_name, v.last_name, v.nickname, v.date_of_birth, v.email, v.phone,
    a.street, a.postal_code, a.city, a.country, ec.name, ec.phone,
  ];
  const canSubmit =
    canSubmitExtra &&
    required.every((x) => (x ?? '').trim() !== '') &&
    !clientInvalid &&
    !loginTaken &&
    (!undeliverable || emailOk) &&
    !orgAsk;

  const loginHint = !login.data
    ? 'Shown instead of the legal name where possible'
    : loginRenamed
      ? `Login name changes from ${initial!.username} to ${login.data.username}; they get an email about it.`
      : `Login name: ${login.data.username}`;

  return (
    <form
      autoComplete="off"
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit(orgFollows ? { ...v, org_email: newOrg } : v);
      }}
      className="space-y-5"
    >
      {before}
      <Section title="Identity" hint="The legal name is used for the account; the nickname is what people see and becomes the login name.">
        <Field label="First name" required error={errs.first_name}>
          <input {...noAutofill} className={inputCls} required value={v.first_name} aria-invalid={!!errs.first_name}
            onChange={(e) => set('first_name', e.target.value)} />
        </Field>
        <Field label="Last name" required error={errs.last_name}>
          <input {...noAutofill} className={inputCls} required value={v.last_name} aria-invalid={!!errs.last_name}
            onChange={(e) => set('last_name', e.target.value)} />
        </Field>
        <Field label="Nickname" required error={errs.nickname} hint={loginHint}>
          <input {...noAutofill} className={inputCls} required value={v.nickname} aria-invalid={!!errs.nickname}
            onChange={(e) => set('nickname', e.target.value)} />
        </Field>
        <Field label="Date of birth" required error={errs.date_of_birth}>
          <input {...noAutofill} className={inputCls} type="date" required value={v.date_of_birth} aria-invalid={!!errs.date_of_birth}
            max={new Date().toISOString().slice(0, 10)} onChange={(e) => set('date_of_birth', e.target.value)} />
        </Field>
        {!hideStatus && (
          <>
            {statusReadOnly ? (
              <Field label="Status" hint="Change it with Pause, Reactivate or Offboard">
                <input className={`${inputCls} bg-muted`} readOnly tabIndex={-1}
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
        <Field as="div" label="Private email" required error={serverErrs.email}>
          <EmailInput value={v.email} onChange={(val) => set('email', val)} acknowledged={emailOk} onAcknowledge={setEmailOk}
            invalid={!!serverErrs.email} inputCls={inputCls} />
        </Field>
        <Field as="div" label="Organization email" error={errs.org_email}>
          <OrgEmailInput className={inputCls} value={v.org_email ?? ''} invalid={!!errs.org_email}
            onChange={(val) => set('org_email', val)} firstName={v.first_name} lastName={v.last_name} nickname={v.nickname ?? ''} />
          {orgFollows && (
            <span className="mt-1 block text-xs text-muted-foreground">Changes to <strong>{newOrg}</strong> with the new name.</span>
          )}
          {orgAsk && (
            <span className="mt-2 flex flex-wrap items-center gap-2 rounded-md border border-warning/40 bg-warning-soft px-2 py-1.5 text-xs text-warning-foreground">
              Also change the organization email to <strong>{newOrg}</strong>?
              <button type="button" onClick={() => set('org_email', newOrg)} className="font-semibold underline">Yes</button>
              <button type="button" onClick={() => setOrgKept(newSug)} className="underline">Keep current</button>
            </span>
          )}
        </Field>
        <Field as="div" label="Phone" required error={errs.phone}>
          <PhoneInput value={v.phone ?? ''} onChange={(val) => set('phone', val)} defaultCountry={settings.data?.default_country}
            invalid={!!errs.phone} />
        </Field>
        <AddressFields value={v.address} onChange={(val) => set('address', val)} defaultCountry={settings.data?.default_country}
          errors={serverErrs} inputCls={inputCls} />
      </Section>

      <Section title="Social handles" hint="Used by teams to reach volunteers during the event. All optional.">
        {SOCIAL_NETWORKS.map((n) => (
          <Field key={n.key} label={n.label} error={errs[`social_handles.${n.key}`]}>
            <input {...noAutofill} className={inputCls} value={v.social_handles?.[n.key] ?? ''} placeholder={n.placeholder}
              onChange={(e) => setSocial(n.key, e.target.value)} />
          </Field>
        ))}
      </Section>

      <Section title="Emergency contact" hint="Who to call if something happens on shift.">
        <Field label="Name" required error={errs['emergency_contact.name']}>
          <input {...noAutofill} className={inputCls} required value={ec.name ?? ''}
            aria-invalid={!!errs['emergency_contact.name']} onChange={(e) => setEmergency('name', e.target.value)} />
        </Field>
        <Field as="div" label="Phone" required error={errs['emergency_contact.phone']}>
          <PhoneInput value={ec.phone ?? ''} onChange={(val) => setEmergency('phone', val)}
            defaultCountry={settings.data?.default_country} invalid={!!errs['emergency_contact.phone']} />
        </Field>
        <Field label="Email" error={errs['emergency_contact.email']}>
          <input {...noAutofill} className={inputCls} type="email" value={ec.email ?? ''}
            aria-invalid={!!errs['emergency_contact.email']} onChange={(e) => setEmergency('email', e.target.value)} />
        </Field>
        <Field label="Comments" hint="Relationship, languages, medical notes they asked us to keep">
          <input {...noAutofill} className={inputCls} value={ec.comments ?? ''} onChange={(e) => setEmergency('comments', e.target.value)} />
        </Field>
      </Section>

      <Section title="Internal notes">
        <Field label="Notes" wide hint="Only visible to HR">
          <textarea {...noAutofill} className={inputCls} rows={3} value={v.notes} onChange={(e) => set('notes', e.target.value)} />
        </Field>
      </Section>

      {error != null && (
        <p className="text-sm text-destructive">
          {(error as Error).message}
          {Object.keys(serverErrs).length > 0 && '. Check the highlighted fields.'}
        </p>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <button type="submit" disabled={pending || !canSubmit}
          className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-60">
          {pending ? 'Saving…' : submitLabel}
        </button>
        {onCancel && (
          <button type="button" onClick={onCancel} className="rounded-pill border border-border px-4 py-2 text-sm">
            Cancel
          </button>
        )}
        <span className="text-xs text-muted-foreground"><span className="text-destructive">*</span> required</span>
        {undeliverable && !emailOk && <span className="text-xs text-warning">Confirm the private email first.</span>}
        {orgAsk && <span className="text-xs text-warning">Answer the organization email question first.</span>}
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

function Field({ label, required, hint, error, wide, as = 'label', children }: {
  label: string;
  required?: boolean;
  hint?: string;
  error?: string;
  wide?: boolean;
  /** "div" for fields made of several controls, so clicks don't jump to the first one. */
  as?: 'label' | 'div';
  children: ReactNode;
}) {
  const Tag = as;
  return (
    <Tag className={`block text-sm ${wide ? 'md:col-span-2' : ''}`}>
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
    </Tag>
  );
}
