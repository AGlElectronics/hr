// Typed fetch wrapper for the HR API.
// Auth: OIDC Bearer (suite) or X-Dev-User via installAuth().

import { runtimeConfig } from '@/runtime-config';

const apiBaseUrl = () =>
  (typeof process !== 'undefined' ? process.env.HR_API_BASE_URL : undefined) ??
  runtimeConfig().apiBaseUrl;

/** One page of a paged list. */
export type Page<T> = { items: T[]; total: number; limit: number; offset: number };

export type ApiErrorBody = {
  error: {
    code: string;
    message: string;
    details?: unknown;
  };
};

export class ApiError extends Error {
  status: number;
  code: string;
  details?: unknown;

  constructor(status: number, body: ApiErrorBody) {
    super(body.error.message);
    this.status = status;
    this.code = body.error.code;
    this.details = body.error.details;
  }
}

type AuthHeaders = Record<string, string>;
let authHeadersResolver: () => Promise<AuthHeaders> = async () => ({});

export function setAuthHeadersResolver(fn: () => Promise<AuthHeaders>) {
  authHeadersResolver = fn;
}

let unauthorizedHandler: (() => void) | null = null;

/** Called when the API answers 401 (session gone); set by the OIDC session sync. */
export function setUnauthorizedHandler(fn: () => void) {
  unauthorizedHandler = fn;
}

type RequestOpts = {
  method?: 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE';
  body?: unknown;
  signal?: AbortSignal;
  query?: Record<string, string | number | boolean | undefined | null>;
  headers?: Record<string, string>;
};

async function send(path: string, opts: RequestOpts, accept: string): Promise<Response> {
  const url = new URL(
    path.startsWith('http') ? path : `${apiBaseUrl()}${path}`,
    window.location.origin,
  );
  if (opts.query) {
    for (const [k, v] of Object.entries(opts.query)) {
      if (v === undefined || v === null) continue;
      url.searchParams.set(k, String(v));
    }
  }

  const form = typeof FormData !== 'undefined' && opts.body instanceof FormData;
  const auth = await authHeadersResolver();
  const headers: Record<string, string> = {
    Accept: accept,
    ...(opts.body !== undefined && !form ? { 'Content-Type': 'application/json' } : {}),
    ...auth,
    ...(opts.headers ?? {}),
  };

  let res: Response;
  try {
    res = await fetch(url.toString(), {
      method: opts.method ?? 'GET',
      body: form ? (opts.body as FormData) : opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
      signal: opts.signal,
      headers,
      credentials: 'include',
    });
  } catch (err) {
    if ((err as Error).name === 'AbortError') throw err;
    throw new ApiError(0, {
      error: { code: 'network', message: 'Cannot reach the HR server. Check your connection or that the stack is running.' },
    });
  }
  if (res.status === 401) {
    unauthorizedHandler?.();
  }
  return res;
}

/** Downloads a file (e.g. an import error report) and returns it as a Blob. */
export async function apiBlob(path: string, opts: RequestOpts = {}): Promise<Blob> {
  const res = await send(path, opts, '*/*');
  if (!res.ok) {
    const text = await res.text();
    let body: Partial<ApiErrorBody> | null = null;
    try {
      body = JSON.parse(text);
    } catch {
      body = null;
    }
    throw new ApiError(res.status, body?.error?.message ? (body as ApiErrorBody) : { error: { code: 'http_error', message: `Request failed (${res.status})` } });
  }
  return res.blob();
}

/** Saves a Blob as a file in the browser. */
export function saveBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}

export async function apiRequest<T>(path: string, opts: RequestOpts = {}): Promise<T> {
  const res = await send(path, opts, 'application/json');
  if (res.status === 204) {
    return undefined as T;
  }

  const text = await res.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    // Proxies and gateways answer with HTML/plain text.
    if (!res.ok) {
      throw new ApiError(res.status, {
        error: { code: 'http_error', message: `The HR server answered ${res.status} ${res.statusText || ''}`.trim() },
      });
    }
    throw new ApiError(res.status, { error: { code: 'bad_response', message: 'The HR server sent an unreadable response.' } });
  }

  if (!res.ok) {
    const body = data as Partial<ApiErrorBody> | null;
    throw new ApiError(
      res.status,
      body?.error?.message
        ? (body as ApiErrorBody)
        : { error: { code: 'http_error', message: `Request failed (${res.status})` } },
    );
  }
  return data as T;
}
