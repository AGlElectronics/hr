import { apiRequest } from './client';
import type { Department } from './departments';
import type { EmploymentDraft, RolePlan } from './drafts';
import type { Person, PersonStatus } from './people';

export type Membership = {
  draft_id: string;
  department?: Department;
  roles: RolePlan[];
  bundle_ids: string[];
};

export type AccountState = {
  /** none: never provisioned · linked · missing: gone from Keycloak · deleted: removed by offboarding · unreachable: Keycloak could not be asked · off: HR keeps records only */
  state: 'none' | 'linked' | 'missing' | 'deleted' | 'unreachable' | 'off';
  username?: string;
  enabled: boolean;
  error?: string;
};

export type PersonAccess = {
  person: Person;
  memberships: Membership[];
  external_id?: string;
  account: AccountState;
  onboardings: EmploymentDraft[];
};

export type OffboardResult = { username: string; warnings: string[] };

/** A person after a status or ban change, plus what HR could not do in the identity provider. */
export type StatusResult = Person & { warnings: string[] };

export const getPersonAccess = (id: string) => apiRequest<PersonAccess>(`/api/people/${id}/access`);

export const changePersonAccess = (id: string, draftId: string, roles: { role_name: string; scope?: string }[], bundleIds?: string[]) =>
  apiRequest<PersonAccess>(`/api/people/${id}/access`, {
    method: 'PUT',
    body: { draft_id: draftId, roles, ...(bundleIds ? { bundle_ids: bundleIds } : {}) },
  });

/** Pausing disables their account; reactivating enables it again (unless banned). */
export const setPersonStatus = (id: string, status: Extract<PersonStatus, 'active' | 'paused'>) =>
  apiRequest<StatusResult>(`/api/people/${id}/status`, { method: 'POST', body: { status } });

export const setPersonBan = (id: string, banned: boolean, reason: string) =>
  apiRequest<StatusResult>(`/api/people/${id}/ban`, { method: 'POST', body: { banned, reason } });

export const resendPasswordSetup = (id: string) =>
  apiRequest<{ sent: boolean }>(`/api/people/${id}/password-setup-email`, { method: 'POST' });

export const offboardPerson = (id: string, body: { reason: string; share_reason?: boolean; hr_only?: boolean }) =>
  apiRequest<OffboardResult>(`/api/people/${id}/offboard`, { method: 'POST', body });
