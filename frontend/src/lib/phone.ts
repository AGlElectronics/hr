import { getCountries, isValidPhoneNumber, parsePhoneNumberFromString, type CountryCode } from 'libphonenumber-js';

const regionNames = typeof Intl !== 'undefined' && 'DisplayNames' in Intl ? new Intl.DisplayNames(['en'], { type: 'region' }) : null;

export function countryName(code: string): string {
  try {
    return regionNames?.of(code) ?? code;
  } catch {
    return code;
  }
}

/** Regional-indicator flag emoji ("AT" -> 🇦🇹). */
export function flag(code: string): string {
  return code.length === 2 ? String.fromCodePoint(...[...code.toUpperCase()].map((c) => 0x1f1a5 + c.charCodeAt(0))) : '';
}

export const COUNTRIES = getCountries()
  .map((c) => ({ code: c, name: countryName(c) }))
  .sort((a, b) => a.name.localeCompare(b.name));

export function toCountry(c: string | undefined): CountryCode | undefined {
  const up = c?.toUpperCase();
  return up && (getCountries() as string[]).includes(up) ? (up as CountryCode) : undefined;
}

/** The region of the browser's preferred languages ("de-AT" → AT), if any. */
export function browserCountry(): CountryCode | undefined {
  for (const lang of navigator.languages ?? [navigator.language]) {
    const c = toCountry(new Intl.Locale(lang).maximize().region);
    if (c) return c;
  }
  return undefined;
}

export function phoneError(v: string | undefined): string | undefined {
  if (!v?.trim()) return undefined;
  return isValidPhoneNumber(v) ? undefined : 'Not a valid phone number for the selected country';
}

/** "+436601234567" -> "+43 660 1234567". */
export function formatPhone(v?: string): string {
  if (!v) return '';
  return parsePhoneNumberFromString(v)?.formatInternational() ?? v;
}
