import { apiRequest, type Page } from './client';

export type PersonStatus = 'applicant' | 'onboarding' | 'active' | 'paused';

export const PERSON_STATUSES: { value: PersonStatus; label: string; hint: string }[] = [
  { value: 'applicant', label: 'Applicant', hint: 'Known to HR, no onboarding started yet' },
  { value: 'onboarding', label: 'Onboarding', hint: 'Onboarding in progress' },
  { value: 'active', label: 'Active', hint: 'Has an account and their access' },
  { value: 'paused', label: 'Paused', hint: 'Taking a break; keeps their profile' },
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

/** Country is an ISO 3166-1 alpha-2 code. */
export type Address = {
  street: string;
  postal_code: string;
  city: string;
  country: string;
};

export const EMPTY_ADDRESS: Address = { street: '', postal_code: '', city: '', country: '' };

export function addressLine(a?: Address): string {
  if (!a) return '';
  const town = [a.postal_code, a.city].filter(Boolean).join(' ');
  return [a.street, town, a.country].filter(Boolean).join(', ');
}

export type Person = {
  id: string;
  first_name: string;
  last_name: string;
  nickname?: string;
  /** Login name of their account. */
  username?: string;
  date_of_birth?: string;
  is_banned: boolean;
  legal_name: string;
  display_name: string;
  email: string;
  org_email?: string;
  phone?: string;
  address: Address;
  social_handles: Record<string, string>;
  emergency_contact: EmergencyContact;
  status: PersonStatus;
  notes?: string;
  /** Account id in the identity provider, and the OIDC subject modules know them by. */
  idp_user_id?: string;
  idp_subject?: string;
  paused_at?: string;
  created_at: string;
  updated_at: string;
  /** Set on update when the profile saved but the account could not be updated. */
  idp_sync_error?: string;
  /** Set on update when the account was updated but the person could not be told. */
  warning?: string;
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
  address: Address;
  social_handles?: Record<string, string>;
  emergency_contact?: EmergencyContact;
  status?: PersonStatus;
  notes?: string;
};

/** The reduced shape of the people list (no date of birth, phone, address or emergency contact). */
export type PersonSummary = Pick<
  Person,
  'id' | 'display_name' | 'legal_name' | 'nickname' | 'username' | 'email' | 'org_email' | 'status' | 'is_banned' | 'updated_at'
>;

export const PAGE_SIZE = 50;

export const listPeople = (filter?: { q?: string; status?: string; department_id?: string; limit?: number; offset?: number }) =>
  apiRequest<Page<PersonSummary>>('/api/people', { query: filter });

export const getPerson = (id: string) => apiRequest<Person>(`/api/people/${id}`);

export const createPerson = (body: PersonInput) =>
  apiRequest<Person>('/api/people', { method: 'POST', body });

export const updatePerson = (id: string, body: PersonInput) =>
  apiRequest<Person>(`/api/people/${id}`, { method: 'PUT', body });

export type EmailCheck = { valid: boolean; deliverable?: boolean; reason?: string };

export const checkEmail = (address: string) =>
  apiRequest<EmailCheck>('/api/check/email', { query: { address } });

export type UsernameCheck = { username: string; available: boolean; reason?: string };

export const checkUsername = (q: { first: string; last: string; nickname: string; person_id?: string }) =>
  apiRequest<UsernameCheck>('/api/check/username', { query: q });

export type AddressHit = Address & { label: string };

/** provider is the server addresses are sent to (shown in the privacy notice). */
export const searchAddress = (q: string) =>
  apiRequest<{ enabled: boolean; items: AddressHit[]; throttled?: boolean; provider?: string }>('/api/address/search', { query: { q } });
