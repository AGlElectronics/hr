import { apiRequest } from './client';

export type PersonStatus = 'applicant' | 'onboarding' | 'active' | 'paused' | 'offboarded';

export const PERSON_STATUSES: { value: PersonStatus; label: string; hint: string }[] = [
  { value: 'applicant', label: 'Applicant', hint: 'Known to HR, no onboarding started yet' },
  { value: 'onboarding', label: 'Onboarding', hint: 'Has an employment draft in progress' },
  { value: 'active', label: 'Active', hint: 'Approved and holding access' },
  { value: 'paused', label: 'Paused', hint: 'Taking a break; keeps their profile' },
  { value: 'offboarded', label: 'Offboarded', hint: 'No longer volunteering' },
];

export const SOCIAL_NETWORKS = [
  { key: 'telegram', label: 'Telegram', placeholder: '@handle' },
  { key: 'discord', label: 'Discord', placeholder: 'username' },
  { key: 'x', label: 'X / Twitter', placeholder: '@handle' },
  { key: 'bluesky', label: 'Bluesky', placeholder: 'name.bsky.social' },
  { key: 'instagram', label: 'Instagram', placeholder: '@handle' },
  { key: 'mastodon', label: 'Mastodon', placeholder: '@user@instance' },
] as const;

export type EmergencyContact = {
  name?: string;
  phone?: string;
  email?: string;
  comments?: string;
};

export type Person = {
  id: string;
  first_name: string;
  last_name: string;
  nickname?: string;
  date_of_birth?: string;
  is_banned: boolean;
  legal_name: string;
  display_name: string;
  email: string;
  email_missing: boolean;
  org_email?: string;
  phone?: string;
  address?: string;
  social_handles: Record<string, string>;
  emergency_contact: EmergencyContact;
  status: PersonStatus;
  notes?: string;
  offboarded_at?: string;
  offboard_reason?: string;
  created_at: string;
  updated_at: string;
  /** Set on update when the profile saved but the Keycloak account could not be synced. */
  idp_sync_error?: string;
};

export type PersonInput = {
  first_name: string;
  last_name: string;
  nickname?: string;
  date_of_birth?: string;
  is_banned?: boolean;
  email: string;
  org_email?: string;
  phone?: string;
  address?: string;
  social_handles?: Record<string, string>;
  emergency_contact?: EmergencyContact;
  status?: PersonStatus;
  notes?: string;
};

export const listPeople = (filter?: { q?: string; status?: string }) =>
  apiRequest<{ items: Person[] }>('/api/people', { query: filter });

export const getPerson = (id: string) => apiRequest<Person>(`/api/people/${id}`);

export const createPerson = (body: PersonInput) =>
  apiRequest<Person>('/api/people', { method: 'POST', body });

export const updatePerson = (id: string, body: PersonInput) =>
  apiRequest<Person>(`/api/people/${id}`, { method: 'PUT', body });
