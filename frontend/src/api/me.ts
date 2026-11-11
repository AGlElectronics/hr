import { apiRequest } from './client';

/** HR permissions (see the backend's authz package). */
export type Permission =
  | 'people.read'
  | 'people.create'
  | 'people.write'
  | 'people.pause'
  | 'people.offboard'
  | 'access.manage'
  | 'access.assign_privileged'
  | 'onboarding.manage'
  | 'departments.manage'
  | 'settings.manage'
  | 'bundles.manage'
  | 'den.manage'
  | 'requirements.manage'
  | 'requirements.delete'
  | 'training.override'
  | 'audit.read'
  | 'review.manage'
  | 'import.run'
  | 'idp.groups.read'
  | 'workspace.manage';

export type Me = {
  username: string;
  auth_mode: string;
  has_access: boolean;
  groups: string[];
  /** HR roles: admin, viewer, lead. */
  roles: string[];
  /** Permission -> "global" (everywhere) or "departments" (only departments the caller leads). */
  permissions: Partial<Record<Permission, 'global' | 'departments'>>;
  /** Departments whose lead list includes the caller. */
  lead_of: string[];
  /** The caller's own HR record, if any. */
  person_id?: string;
};

export const getMe = () => apiRequest<Me>('/api/me');

/** Whether the caller holds the permission anywhere (global or as a lead). */
export const can = (me: Me | undefined, p: Permission) => !!me?.permissions?.[p];

/** Whether the caller holds the permission everywhere. */
export const canGlobal = (me: Me | undefined, p: Permission) => me?.permissions?.[p] === 'global';
