import { fireEvent, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Permission } from '@/api/me';
import { fakeApiRequest, me, renderWithQuery, routerMock, setRoutes } from '@/test/helpers';

vi.mock('@/api/client', async (orig) => ({ ...(await orig<object>()), apiRequest: fakeApiRequest }));
vi.mock('@tanstack/react-router', () => routerMock);

const { PersonActions } = await import('./PersonActions');

const access = {
  person: { id: 'p1', display_name: 'Paula', status: 'active', is_banned: false },
  memberships: [],
  onboardings: [],
  account: { state: 'linked', enabled: true, username: 'paula' },
};

function settings(staffMode: string) {
  return {
    identity: { staff_mode: '', sso_name: '', sso_url: '', admin_console_url: '', effective: { staff_mode: staffMode, sso_name: 'Example Con SSO', sso_url: 'https://sso.example.org', admin_console_url: '' } },
  };
}

function routes(perms: Permission[], extra: Record<string, unknown> = {}) {
  setRoutes({
    'GET /api/people/p1/access': access,
    'GET /api/settings': settings('own'),
    'GET /api/me': me(perms),
    'GET /api/access-catalog': { modules: [] },
    ...extra,
  });
}

const ALL: Permission[] = ['people.read', 'people.pause', 'people.offboard', 'access.manage', 'onboarding.manage'];

describe('PersonActions', () => {
  beforeEach(() => routes(ALL));

  it('shows the actions an HR admin may take', async () => {
    renderWithQuery(<PersonActions personId="p1" onRemoved={vi.fn()} />);
    expect(await screen.findByRole('button', { name: /Pause/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Ban/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Offboard/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Resend set-password email/ })).toBeInTheDocument();
  });

  it('shows no actions to a read-only viewer', async () => {
    routes(['people.read']);
    renderWithQuery(<PersonActions personId="p1" onRemoved={vi.fn()} />);
    expect(await screen.findByText('Departments & access')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Pause|Ban|Offboard|Resend/ })).not.toBeInTheDocument();
  });

  it('does not let HR users change themselves', async () => {
    routes(ALL, { 'GET /api/me': me(ALL, { person_id: 'p1' }) });
    renderWithQuery(<PersonActions personId="p1" onRemoved={vi.fn()} />);
    expect(await screen.findByText(/This is you/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Pause|Ban|Offboard/ })).not.toBeInTheDocument();
  });

  it('warns that offboarding keeps the SSO account in brokered mode', async () => {
    routes(ALL, { 'GET /api/settings': settings('brokered') });
    renderWithQuery(<PersonActions personId="p1" onRemoved={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: /Offboard/ }));
    expect(await screen.findByText('Offboarding in HR does not close the SSO account')).toBeInTheDocument();
  });

  it('has no SSO warning with Con Services accounts', async () => {
    renderWithQuery(<PersonActions personId="p1" onRemoved={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: /Offboard/ }));
    expect(screen.getByText('Offboard Paula')).toBeInTheDocument();
    expect(screen.queryByText('Offboarding in HR does not close the SSO account')).not.toBeInTheDocument();
  });

  it('asks for a reason before banning', async () => {
    renderWithQuery(<PersonActions personId="p1" onRemoved={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: /Ban/ }));
    const submit = screen.getAllByRole('button', { name: 'Ban' }).at(-1)!;
    expect(submit).toBeDisabled();
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'harassment' } });
    expect(submit).toBeEnabled();
  });
});
