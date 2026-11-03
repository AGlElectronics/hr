import { apiRequest } from './client';
import type { Department, RoleRef } from './departments';
import type { EmploymentDraft, RolePlan } from './drafts';
import type { Person, PersonStatus } from './people';

export type Membership = {
  draft_id: string;
  department?: Department;
  roles: RolePlan[];
};

export type AccountState = {
  /** none: never provisioned · linked · missing: deleted in Keycloak · unreachable: Keycloak could not be asked */
  state: 'none' | 'linked' | 'missing' | 'unreachable';
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

export type OffboardResult = { person: Person; warnings: string[] };

export const getPersonAccess = (id: string) => apiRequest<PersonAccess>(`/api/people/${id}/access`);

export const changePersonAccess = (id: string, draftId: string, roles: RoleRef[]) =>
  apiRequest<PersonAccess>(`/api/people/${id}/access`, { method: 'PUT', body: { draft_id: draftId, roles } });

export const setPersonStatus = (id: string, status: Extract<PersonStatus, 'active' | 'paused'>) =>
  apiRequest<Person>(`/api/people/${id}/status`, { method: 'POST', body: { status } });

export const offboardPerson = (id: string, body: { reason: string; keep_account?: boolean; hr_only?: boolean }) =>
  apiRequest<OffboardResult>(`/api/people/${id}/offboard`, { method: 'POST', body });
