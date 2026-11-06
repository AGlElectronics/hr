import { useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { MapPin } from 'lucide-react';

import { searchAddress, type Address } from '@/api/people';
import { noAutofill } from '@/lib/noAutofill';
import { COUNTRIES } from '@/lib/phone';
import { cn } from '@/lib/utils';

function useDebounced<T>(v: T, ms: number): T {
  const [d, setD] = useState(v);
  useEffect(() => {
    const t = setTimeout(() => setD(v), ms);
    return () => clearTimeout(t);
  }, [v, ms]);
  return d;
}

/**
 * Street / postal code / city / country. Typing in the street field offers
 * matches from the configured address lookup; everything stays editable by hand.
 */
export function AddressFields({
  value,
  onChange,
  defaultCountry,
  errors,
  inputCls,
}: {
  value: Address;
  onChange: (a: Address) => void;
  defaultCountry?: string;
  errors: Record<string, string>;
  inputCls: string;
}) {
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  const typed = useRef(false);
  const q = useDebounced(value.street.trim(), 350);
  const lookup = useQuery({
    queryKey: ['address-search', q],
    queryFn: () => searchAddress(q),
    enabled: open && typed.current && q.length >= 3,
    staleTime: 5 * 60_000,
    retry: false,
  });
  const hits = open ? (lookup.data?.items ?? []) : [];
  const set = (k: keyof Address, v: string) => onChange({ ...value, [k]: v });

  useEffect(() => {
    if (!value.country && defaultCountry) onChange({ ...value, country: defaultCountry.toUpperCase() });
    // Only fills an empty country once settings arrive.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [defaultCountry]);

  const pick = (i: number) => {
    const h = hits[i];
    if (!h) return;
    onChange({
      street: h.street || value.street,
      postal_code: h.postal_code || value.postal_code,
      city: h.city || value.city,
      country: h.country || value.country,
    });
    typed.current = false;
    setOpen(false);
  };

  const err = (k: string) => errors[`address.${k}`] ?? (k === 'street' ? errors.address : undefined);
  const lookupOff = lookup.data && !lookup.data.enabled;

  return (
    <div className="grid gap-3 md:col-span-2 md:grid-cols-[2fr_1fr_1.5fr_1.5fr]">
      <div className="relative text-sm">
        <span className="mb-1 block font-medium">Street and number <span className="text-destructive">*</span></span>
        <input
          {...noAutofill}
          role="combobox"
          aria-expanded={hits.length > 0}
          aria-autocomplete="list"
          aria-invalid={!!err('street')}
          className={inputCls}
          value={value.street}
          placeholder="Start typing to search"
          onChange={(e) => {
            typed.current = true;
            setActive(-1);
            setOpen(true);
            set('street', e.target.value);
          }}
          onFocus={() => setOpen(true)}
          onBlur={() => setTimeout(() => setOpen(false), 150)}
          onKeyDown={(e) => {
            if (!hits.length) return;
            if (e.key === 'ArrowDown') {
              e.preventDefault();
              setActive((a) => Math.min(a + 1, hits.length - 1));
            } else if (e.key === 'ArrowUp') {
              e.preventDefault();
              setActive((a) => Math.max(a - 1, 0));
            } else if (e.key === 'Enter' && active >= 0) {
              e.preventDefault();
              pick(active);
            } else if (e.key === 'Escape') {
              setOpen(false);
            }
          }}
        />
        {hits.length > 0 && (
          <ul role="listbox" className="absolute z-30 mt-1 max-h-64 w-[min(32rem,90vw)] overflow-y-auto rounded-md border border-border bg-card py-1 shadow-lg">
            {hits.map((h, i) => (
              <li key={`${h.label}-${i}`} role="option" aria-selected={i === active}>
                <button type="button" onMouseDown={(e) => e.preventDefault()} onClick={() => pick(i)}
                  className={cn('flex w-full items-start gap-2 px-3 py-1.5 text-left text-sm hover:bg-muted', i === active && 'bg-muted')}>
                  <MapPin className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                  {h.label}
                </button>
              </li>
            ))}
            <li className="px-3 pt-1 text-[10px] text-muted-foreground">© OpenStreetMap contributors</li>
          </ul>
        )}
        {err('street') ? (
          <span className="mt-1 block text-xs text-destructive">{err('street')}</span>
        ) : lookupOff ? (
          <span className="mt-1 block text-xs text-muted-foreground">Address search is off; enter it by hand.</span>
        ) : null}
      </div>
      <Small label="Postal code" error={err('postal_code')}>
        <input {...noAutofill} className={inputCls} value={value.postal_code} aria-invalid={!!err('postal_code')}
          onChange={(e) => set('postal_code', e.target.value)} />
      </Small>
      <Small label="City" error={err('city')}>
        <input {...noAutofill} className={inputCls} value={value.city} aria-invalid={!!err('city')}
          onChange={(e) => set('city', e.target.value)} />
      </Small>
      <Small label="Country" error={err('country')}>
        <select className={inputCls} value={value.country} aria-invalid={!!err('country')}
          onChange={(e) => set('country', e.target.value)}>
          <option value="">Choose…</option>
          {COUNTRIES.map((c) => <option key={c.code} value={c.code}>{c.name}</option>)}
        </select>
      </Small>
    </div>
  );
}

function Small({ label, error, children }: { label: string; error?: string; children: React.ReactNode }) {
  return (
    <label className="block text-sm">
      <span className="mb-1 block font-medium">{label} <span className="text-destructive">*</span></span>
      {children}
      {error && <span className="mt-1 block text-xs text-destructive">{error}</span>}
    </label>
  );
}
