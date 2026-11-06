const norm = (s: string) => s.toLowerCase().normalize('NFKD').replace(/[\u0300-\u036f]/g, '');

/** Case- and accent-insensitive "every word appears somewhere" match. */
export function matches(q: string, ...fields: (string | undefined | null)[]): boolean {
  const hay = norm(fields.filter(Boolean).join(' '));
  return norm(q).split(/\s+/).filter(Boolean).every((w) => hay.includes(w));
}
