import { apiRequest } from './client';

export type IdPGroupMember = {
  id: string;
  username: string;
  email?: string;
  first_name?: string;
  last_name?: string;
  enabled: boolean;
};

export type IdPGroup = {
  id: string;
  name: string;
  path: string;
  members: IdPGroupMember[];
  department?: { id: string; name: string };
  default_for?: string[];
};

export const listIdPGroups = () => apiRequest<{ available: boolean; groups: IdPGroup[] }>('/api/idp/groups');

/** Keycloak admin console for the realm HR provisions into, derived from the OIDC authority. */
export function keycloakConsoleUrl(section: 'groups' | 'users' = 'groups'): string {
  const authority =
    (import.meta.env.VITE_OIDC_AUTHORITY as string | undefined)?.trim() || 'http://localhost:8085/realms/conservices';
  const m = authority.match(/^(.*)\/realms\/([^/]+)\/?$/);
  const base = m ? m[1] : 'http://localhost:8085';
  const realm = m ? m[2] : 'conservices';
  return `${base}/admin/master/console/#/${realm}/${section}`;
}
