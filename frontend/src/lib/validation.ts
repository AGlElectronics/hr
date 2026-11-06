// Mirrors backend/internal/service/validate.go.

const NAME_RE = /^\p{L}[\p{L}\p{M} .'’-]*$/u;

/** Letters (with accents), spaces, periods, hyphens and apostrophes; no emoji or symbols. */
export function nameError(s: string | undefined): string | undefined {
  const t = (s ?? '').trim();
  if (!t) return undefined;
  return NAME_RE.test(t) ? undefined : 'Use letters, spaces, hyphens, apostrophes or periods only';
}

const EMAIL_RE = /^[A-Za-z0-9.!#$%&'*+/=?^_`{|}~-]+@([A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,}$/;

export function emailError(s: string | undefined): string | undefined {
  const t = (s ?? '').trim();
  if (!t) return undefined;
  return EMAIL_RE.test(t) ? undefined : 'This is not a valid email address';
}

const COMMON_DOMAINS = [
  'gmail.com', 'googlemail.com', 'outlook.com', 'hotmail.com', 'live.com', 'msn.com', 'yahoo.com', 'icloud.com',
  'me.com', 'proton.me', 'protonmail.com', 'gmx.at', 'gmx.de', 'gmx.net', 'web.de', 't-online.de', 'aon.at',
  'aol.com', 'mail.com', 'yandex.com', 'zoho.com', 'fastmail.com', 'tutanota.com', 'posteo.de',
];

function distance(a: string, b: string): number {
  const d = Array.from({ length: a.length + 1 }, (_, i) => [i, ...Array(b.length).fill(0)]);
  for (let j = 1; j <= b.length; j++) d[0][j] = j;
  for (let i = 1; i <= a.length; i++)
    for (let j = 1; j <= b.length; j++) {
      d[i][j] = Math.min(d[i - 1][j] + 1, d[i][j - 1] + 1, d[i - 1][j - 1] + (a[i - 1] === b[j - 1] ? 0 : 1));
      if (i > 1 && j > 1 && a[i - 1] === b[j - 2] && a[i - 2] === b[j - 1]) d[i][j] = Math.min(d[i][j], d[i - 2][j - 2] + 1);
    }
  return d[a.length][b.length];
}

/** "jane@gmial.com" -> "jane@gmail.com"; undefined when nothing close. */
export function suggestEmail(s: string): string | undefined {
  const [local, domain] = s.trim().toLowerCase().split('@');
  if (!local || !domain || COMMON_DOMAINS.includes(domain)) return undefined;
  let best: string | undefined;
  let bestD = 3;
  for (const c of COMMON_DOMAINS) {
    const dd = distance(domain, c);
    if (dd > 0 && dd < bestD) {
      best = c;
      bestD = dd;
    }
  }
  return best && `${s.trim().split('@')[0]}@${best}`;
}
