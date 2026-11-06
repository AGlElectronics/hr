// Mirrors backend/internal/service/naming.go.

export const DEFAULT_USERNAME_TEMPLATE = '{nickname}';
export const DEFAULT_ORG_EMAIL_TEMPLATE = '{first}.{last}';
const FALLBACK_TEMPLATE = '{first}.{last}';

export type NameParts = { first: string; last: string; nickname: string };

/** Lowercase ASCII ("Zoë Mary-Ann O'Neil" -> "zoe.mary-ann.oneil"). */
export function slug(s: string): string {
  return s
    .trim()
    .toLowerCase()
    .normalize('NFKD')
    .replace(/[\u0300-\u036f]/g, '')
    .replace(/['’]/g, '')
    .replace(/\s+/g, '.')
    .replace(/[^a-z0-9._-]+/g, '')
    .replace(/([._-])[._-]+/g, '$1')
    .replace(/^[._-]+|[._-]+$/g, '');
}

/** Fills {first} {last} {nickname}; "" when any used placeholder is empty. */
export function renderTemplate(tpl: string, p: NameParts): string {
  let missing = false;
  const out = tpl.replace(/\{[^}]*\}/g, (t) => {
    const v = slug(t === '{first}' ? p.first : t === '{last}' ? p.last : t === '{nickname}' ? p.nickname : '');
    if (!v) missing = true;
    return v;
  });
  return missing ? '' : slug(out);
}

/** Template first, then first.last. */
export function renderWithFallback(tpl: string | undefined, p: NameParts): string {
  for (const t of [tpl?.trim() || FALLBACK_TEMPLATE, FALLBACK_TEMPLATE]) {
    const v = renderTemplate(t, p);
    if (v) return v;
  }
  return '';
}
