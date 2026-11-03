import { apiRequest } from './client';

export type Me = { username: string; auth_mode: string; has_access: boolean; groups: string[] };

export const getMe = () => apiRequest<Me>('/api/me');
