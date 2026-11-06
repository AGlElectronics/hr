import { useMemo, useState } from 'react';
import { getCountryCallingCode, parsePhoneNumberFromString, type CountryCode } from 'libphonenumber-js';
import { ChevronDown } from 'lucide-react';

import { noAutofill } from '@/lib/noAutofill';
import { browserCountry, COUNTRIES, countryName, flag, toCountry } from '@/lib/phone';
import { cn } from '@/lib/utils';

/**
 * Country picker (flag + dialling code) and number. Emits E.164
 * ("+436601234567") once the number is valid for the country, the typed
 * text otherwise so the form can flag it.
 */
export function PhoneInput({
  value,
  onChange,
  defaultCountry,
  invalid,
  id,
}: {
  value: string;
  onChange: (v: string) => void;
  defaultCountry?: string;
  invalid?: boolean;
  id?: string;
}) {
  const parsed = useMemo(() => (value ? parsePhoneNumberFromString(value) : undefined), [value]);
  const [country, setCountry] = useState<CountryCode>(
    () => parsed?.country ?? toCountry(defaultCountry) ?? browserCountry() ?? 'US',
  );
  const [text, setText] = useState(() => (parsed?.isValid() ? parsed.formatNational() : value));

  const emit = (raw: string, c: CountryCode) => {
    const p = parsePhoneNumberFromString(raw, c);
    if (p?.country && p.country !== c && raw.trim().startsWith('+')) setCountry(p.country);
    onChange(p?.isValid() ? p.number : raw.trim());
  };

  return (
    <div className={cn(
      'flex items-stretch overflow-hidden rounded-md border border-input bg-background focus-within:ring-2 focus-within:ring-ring',
      invalid && 'border-destructive',
    )}>
      <label className="relative flex shrink-0 items-center gap-1 border-r border-input bg-muted px-2 text-sm" title={countryName(country)}>
        <span aria-hidden>{flag(country)}</span>
        <span className="tabular-nums text-muted-foreground">+{getCountryCallingCode(country)}</span>
        <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
        <select
          aria-label="Country"
          className="absolute inset-0 cursor-pointer opacity-0"
          value={country}
          onChange={(e) => {
            const c = e.target.value as CountryCode;
            setCountry(c);
            if (text) emit(text, c);
          }}
        >
          {COUNTRIES.map((c) => (
            <option key={c.code} value={c.code}>
              {c.name} (+{getCountryCallingCode(c.code)})
            </option>
          ))}
        </select>
      </label>
      <input
        id={id}
        type="tel"
        {...noAutofill}
        aria-invalid={invalid}
        className="min-w-0 flex-1 bg-transparent px-3 py-2 text-sm outline-none"
        value={text}
        placeholder="Phone number"
        onChange={(e) => {
          setText(e.target.value);
          emit(e.target.value, country);
        }}
        onBlur={() => {
          const p = parsePhoneNumberFromString(text, country);
          if (p?.isValid()) setText(p.country === country ? p.formatNational() : p.formatInternational());
        }}
      />
    </div>
  );
}
