import { apiRequest } from './client';

export type Settings = { org_email_domain: string };

export const getSettings = () => apiRequest<Settings>('/api/settings');

export const updateSettings = (body: Settings) => apiRequest<Settings>('/api/settings', { method: 'PUT', body });
