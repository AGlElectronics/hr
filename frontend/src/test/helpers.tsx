import type { ReactElement, ReactNode } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render } from '@testing-library/react';
import { vi } from 'vitest';

import type { Me, Permission } from '@/api/me';

type Handler = unknown | ((body: unknown) => unknown);

/** Calls made through the mocked apiRequest: "METHOD /path" and body. */
export const calls: { key: string; body: unknown }[] = [];
let routes: Record<string, Handler> = {};

/** Answers for the mocked apiRequest, keyed "METHOD /path" (query string ignored). */
export function setRoutes(r: Record<string, Handler>) {
  routes = r;
  calls.length = 0;
}

export const fakeApiRequest = vi.fn(async (path: string, opts: { method?: string; body?: unknown } = {}) => {
  const key = `${opts.method ?? 'GET'} ${path}`;
  calls.push({ key, body: opts.body });
  if (!(key in routes)) throw new Error(`unexpected request ${key}`);
  const h = routes[key];
  return typeof h === 'function' ? (h as (b: unknown) => unknown)(opts.body) : h;
});

/** A signed-in HR user with these permissions (global unless "departments"). */
export function me(perms: Permission[], extra: Partial<Me> = {}): Me {
  return {
    username: 'tester',
    auth_mode: 'dev',
    has_access: true,
    groups: [],
    roles: [],
    permissions: Object.fromEntries(perms.map((p) => [p, 'global'])),
    lead_of: [],
    ...extra,
  };
}

export function renderWithQuery(ui: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  return render(ui, { wrapper });
}

/** Router stand-ins: links become anchors, params come from the test. */
export const routerMock = {
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
  useNavigate: () => vi.fn(),
  useParams: () => ({ id: 'draft-1' }),
};
