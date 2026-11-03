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
};

export type CatalogModule = {
  module: string;
  display_name: string;
  description: string;
  roles: CatalogRole[];
  source: string;
  status: 'ok' | 'error';
  error?: string;
};

export const getAccessCatalog = () =>
  apiRequest<{ modules: CatalogModule[] }>('/api/access-catalog');

/** Draft role_name encoding: "<module>:<role key>". */
export const roleRef = (module: string, key: string) => `${module}:${key}`;
