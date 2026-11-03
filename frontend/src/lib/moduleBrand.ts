// Den branding for Con Services modules. Identical in HR, Stash and Rally;
// keep the copies in sync.
//
// The backend serves GET <api>/brand: Den's theme when paired, an empty
// theme when standalone (then styles.css defaults apply). Den's theme holds
// hex colours for light mode; dark mode keeps the module's dark surfaces and
// only takes Den's primary hue, so it stays readable.
import { useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';

export type ModuleBrand = {
  source: 'den' | 'standalone';
  revision: number;
  organization_name: string;
  logo_url?: string;
  theme: Record<string, unknown>;
  copy: Record<string, unknown>;
};

type HSL = [number, number, number];

function hexToHsl(v: unknown): HSL | null {
  if (typeof v !== 'string') return null;
  let h = v.trim().replace(/^#/, '');
  if (h.length === 3) h = h.split('').map((c) => c + c).join('');
  if (!/^[0-9a-f]{6}$/i.test(h)) return null;
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16) / 255);
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const l = (max + min) / 2;
  let hue = 0;
  let s = 0;
  if (max !== min) {
    const d = max - min;
    s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
    hue = max === r ? (g - b) / d + (g < b ? 6 : 0) : max === g ? (b - r) / d + 2 : (r - g) / d + 4;
    hue *= 60;
  }
  return [Math.round(hue), Math.round(s * 100), Math.round(l * 100)];
}

const css = ([h, s, l]: HSL) => `${h} ${s}% ${l}%`;
const clamp = (n: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, n));

/** Readable text colour on top of an HSL colour. */
function onColor([h, s, l]: HSL): string {
  const lin = (c: number) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
  const a = (s / 100) * Math.min(l / 100, 1 - l / 100);
  const f = (n: number) => {
    const k = (n + h / 30) % 12;
    return l / 100 - a * Math.max(-1, Math.min(k - 3, 9 - k, 1));
  };
  const lum = 0.2126 * lin(f(0)) + 0.7152 * lin(f(8)) + 0.0722 * lin(f(4));
  return lum > 0.45 ? '240 10% 10%' : '0 0% 100%';
}

/** CSS for :root and .dark from Den's theme JSON ("" when nothing applies). */
export function brandCss(theme: Record<string, unknown>): string {
  const light: Record<string, string> = {};
  const dark: Record<string, string> = {};
  const primary = hexToHsl(theme.primary);
  if (primary) {
    const [h, s, l] = primary;
    light['--primary'] = light['--ring'] = css(primary);
    light['--primary-foreground'] = onColor(primary);
    light['--primary-hover'] = css(hexToHsl(theme.primary_hover) ?? [h, s, clamp(l - 7, 5, 95)]);
    light['--accent-foreground'] = css([h, clamp(s, 30, 70), 35]);
    const darkPrimary: HSL = [h, s, clamp(l, 55, 75)];
    dark['--primary'] = dark['--ring'] = css(darkPrimary);
    dark['--primary-foreground'] = onColor(darkPrimary);
    dark['--primary-hover'] = css([h, s, clamp(darkPrimary[2] + 6, 0, 85)]);
    dark['--primary-soft'] = css([h, 35, 21]);
    dark['--accent'] = css([h, 28, 21]);
    dark['--accent-foreground'] = css([h, 85, 82]);
  }
  const map: [string, string[]][] = [
    ['accent', ['--primary-soft', '--accent']],
    ['background', ['--background', '--card', '--popover']],
    ['muted', ['--muted', '--secondary']],
    ['foreground', ['--foreground', '--card-foreground', '--popover-foreground', '--secondary-foreground']],
    ['border', ['--border', '--input']],
  ];
  for (const [key, vars] of map) {
    const c = hexToHsl(theme[key]);
    if (c) for (const v of vars) light[v] = css(c);
  }
  const block = (sel: string, vars: Record<string, string>) =>
    Object.keys(vars).length
      ? `${sel} {\n${Object.entries(vars).map(([k, v]) => `  ${k}: ${v};`).join('\n')}\n}\n`
      : '';
  return block(':root', light) + block('.dark', dark);
}

function applyBrand(brand: ModuleBrand | undefined, productName: string | null) {
  let el = document.getElementById('brand') as HTMLStyleElement | null;
  if (!el) {
    el = document.createElement('style');
    el.id = 'brand';
    document.head.appendChild(el);
  }
  el.textContent = brand ? brandCss(brand.theme ?? {}) : '';
  if (productName === null) return;
  const org = brand?.organization_name?.trim();
  document.title = org ? `${productName} · ${org}` : productName;
}

async function fetchBrand(endpoint: string): Promise<ModuleBrand> {
  const res = await fetch(endpoint, { cache: 'no-store', headers: { Accept: 'application/json' } });
  if (!res.ok) throw new Error(`brand: HTTP ${res.status}`);
  return (await res.json()) as ModuleBrand;
}

/**
 * Loads and applies the brand; refetches every minute and on tab focus so a
 * change in Den shows up without a rebuild or reload. Pass productName null
 * when the module sets the document title itself.
 */
export function useModuleBrand(endpoint: string, productName: string | null): ModuleBrand | undefined {
  const q = useQuery({
    queryKey: ['module-brand', endpoint],
    queryFn: () => fetchBrand(endpoint),
    staleTime: 30_000,
    refetchInterval: 60_000,
    refetchOnWindowFocus: true,
    retry: 1,
  });
  useEffect(() => applyBrand(q.data, productName), [q.data, productName]);
  return q.data;
}
