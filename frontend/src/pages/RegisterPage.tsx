import { useEffect, useState, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Hourglass } from 'lucide-react';

import { getSelfService, listSelfServiceDepartments, submitSelfService, type SelfServiceInput } from '@/api/admin';
import { ApiError } from '@/api/client';
import { EMPTY_ADDRESS } from '@/api/people';

const inputCls = 'w-full rounded-md border border-input bg-background px-3 py-2';

const EMPTY: SelfServiceInput = {
  first_name: '',
  last_name: '',
  nickname: '',
  phone: '',
  date_of_birth: '',
  address: EMPTY_ADDRESS,
  emergency_contact: {},
};

const STATUS_TEXT: Record<string, string> = {
  pending: 'Some details are still missing. Add them below so a coordinator can review you.',
  ready: 'Thanks! A coordinator will review your details. You can still change them until then.',
  accepted: 'A coordinator accepted you. Your access is being set up; sign out and in again in a few minutes.',
  merged: 'A coordinator linked your account to your existing record.',
  rejected: 'A coordinator could not accept this registration. Contact your coordinator if you think this is a mistake.',
};

/**
 * "Waiting for a coordinator": a signed-in volunteer without an HR record
 * tells HR who they are. It only ever reads and changes their own entry.
 */
export function RegisterPage() {
  const qc = useQueryClient();
  const current = useQuery({
    queryKey: ['self-service'],
    queryFn: async () => {
      try {
        return await getSelfService();
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) return null;
        throw e;
      }
    },
  });
  const depts = useQuery({ queryKey: ['self-service-departments'], queryFn: listSelfServiceDepartments });
  const [form, setForm] = useState<SelfServiceInput>(EMPTY);
  useEffect(() => {
    if (current.data?.item) setForm({ ...EMPTY, ...current.data.item, address: current.data.item.address ?? EMPTY_ADDRESS });
  }, [current.data]);
  const submit = useMutation({ mutationFn: () => submitSelfService(form), onSuccess: (v) => qc.setQueryData(['self-service'], v) });
  const errs = submit.error instanceof ApiError ? ((submit.error.details as Record<string, string> | undefined) ?? {}) : {};
  const status = current.data?.status;
  const locked = status === 'accepted' || status === 'merged' || status === 'rejected';
  const set = <K extends keyof SelfServiceInput>(k: K, v: SelfServiceInput[K]) => setForm((f) => ({ ...f, [k]: v }));

  return (
    <div className="min-h-screen bg-background p-6 text-foreground">
      <div className="mx-auto max-w-xl space-y-4 rounded-lg border border-border bg-card p-6">
        <h1 className="flex items-center gap-2 text-xl font-bold"><Hourglass className="h-5 w-5 text-primary" /> Waiting for a coordinator</h1>
        <p className="text-sm text-muted-foreground">
          You are signed in, but HR does not know you yet. Fill in your details; a coordinator checks them and gives you access.
        </p>
        {current.isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
        {current.error && <p role="alert" className="text-sm text-destructive">{(current.error as Error).message}</p>}
        {status && <p className="rounded-md border border-border bg-muted/60 px-3 py-2 text-sm">{STATUS_TEXT[status]}</p>}
        {current.data?.email && <p className="text-sm">Email: <strong>{current.data.email}</strong></p>}

        <form className="space-y-3" autoComplete="on" onSubmit={(e) => { e.preventDefault(); submit.mutate(); }}>
          <fieldset disabled={locked || current.isLoading} className="space-y-3">
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="First name" error={errs.first_name}>
                <input className={inputCls} autoComplete="given-name" value={form.first_name} onChange={(e) => set('first_name', e.target.value)} />
              </Field>
              <Field label="Last name" error={errs.last_name}>
                <input className={inputCls} autoComplete="family-name" value={form.last_name} onChange={(e) => set('last_name', e.target.value)} />
              </Field>
              <Field label="Nickname (optional)" error={errs.nickname}>
                <input className={inputCls} autoComplete="nickname" value={form.nickname} onChange={(e) => set('nickname', e.target.value)} />
              </Field>
              <Field label="Phone" error={errs.phone}>
                <input className={inputCls} type="tel" autoComplete="tel" value={form.phone} onChange={(e) => set('phone', e.target.value)} />
              </Field>
              <Field label="Date of birth" error={errs.date_of_birth}>
                <input className={inputCls} type="date" autoComplete="bday" value={form.date_of_birth} onChange={(e) => set('date_of_birth', e.target.value)} />
              </Field>
              <Field label="Department you'd like to help (optional)" error={errs.department_id}>
                <select className={inputCls} value={form.department_id ?? ''} onChange={(e) => set('department_id', e.target.value || undefined)}>
                  <option value="">No preference</option>
                  {depts.data?.items.map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
                </select>
              </Field>
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="Street and number" error={errs['address.street']}>
                <input className={inputCls} autoComplete="street-address" value={form.address.street}
                  onChange={(e) => set('address', { ...form.address, street: e.target.value })} />
              </Field>
              <Field label="Postal code" error={errs['address.postal_code']}>
                <input className={inputCls} autoComplete="postal-code" value={form.address.postal_code}
                  onChange={(e) => set('address', { ...form.address, postal_code: e.target.value })} />
              </Field>
              <Field label="City" error={errs['address.city']}>
                <input className={inputCls} autoComplete="address-level2" value={form.address.city}
                  onChange={(e) => set('address', { ...form.address, city: e.target.value })} />
              </Field>
              <Field label="Country (2 letters, e.g. NL)" error={errs['address.country']}>
                <input className={inputCls} autoComplete="country" maxLength={2} value={form.address.country}
                  onChange={(e) => set('address', { ...form.address, country: e.target.value.toUpperCase() })} />
              </Field>
              <Field label="Emergency contact name" error={errs['emergency_contact.name']}>
                <input className={inputCls} value={form.emergency_contact.name ?? ''}
                  onChange={(e) => set('emergency_contact', { ...form.emergency_contact, name: e.target.value })} />
              </Field>
              <Field label="Emergency contact phone" error={errs['emergency_contact.phone']}>
                <input className={inputCls} type="tel" value={form.emergency_contact.phone ?? ''}
                  onChange={(e) => set('emergency_contact', { ...form.emergency_contact, phone: e.target.value })} />
              </Field>
            </div>
          </fieldset>
          {submit.error && Object.keys(errs).length === 0 && <p role="alert" className="text-sm text-destructive">{(submit.error as Error).message}</p>}
          {!locked && (
            <button type="submit" disabled={submit.isPending}
              className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-60">
              {submit.isPending ? 'Sending…' : status ? 'Update my details' : 'Send to a coordinator'}
            </button>
          )}
        </form>
      </div>
    </div>
  );
}

function Field({ label, error, children }: { label: string; error?: string; children: ReactNode }) {
  return (
    <label className="block text-sm">
      <span className="mb-1 block font-medium">{label}</span>
      {children}
      {error && <span className="mt-1 block text-xs text-destructive">{error}</span>}
    </label>
  );
}
