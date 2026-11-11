import { apiRequest } from './client';

export type CatalogRole = {
  key: string;
  label: string;
  description: string;
  group: string;
  assignable: boolean;
  scope: string;
  permissions: string[];
  note?: string;
  /** Pre-selected for everyone (e.g. Den: Member); can be unticked. */
  default?: boolean;
  /** Administration of a module: needs the "assign admin roles" permission. */
  privileged?: boolean;
};

export type CatalogModule = {
  module: string;
  display_name: string;
  description: string;
  roles: CatalogRole[];
  source: string;
  /** ok: live · cached: module offline, last known catalog · error: offline and never seen. */
  status: 'ok' | 'cached' | 'error';
  error?: string;
  fetched_at?: string;
};

export const getAccessCatalog = () =>
  apiRequest<{ modules: CatalogModule[] }>('/api/access-catalog');

/** Draft role_name encoding: "<module>:<role key>". */
export const roleRef = (module: string, key: string) => `${module}:${key}`;

/** Admin-type roles: flagged privileged, or named admin / *-admin. */
export const isAdminRole = (r: Pick<CatalogRole, 'key' | 'group' | 'privileged'>) =>
  !!r.privileged || r.key === 'admin' || r.key.endsWith('-admin') || r.group.endsWith('-admin');
