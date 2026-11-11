import { fireEvent, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { fakeApiRequest, me, renderWithQuery, setRoutes } from '@/test/helpers';

vi.mock('@/api/client', async (orig) => ({ ...(await orig<object>()), apiRequest: fakeApiRequest }));

const { AccessPicker } = await import('./AccessPicker');

const catalog = {
  modules: [
    {
      module: 'stash',
      display_name: 'Stash',
      description: 'Inventory',
      source: 'den',
      status: 'ok',
      roles: [
        { key: 'volunteer', label: 'Volunteer', description: '', group: 'stash-volunteer', assignable: true, scope: 'global', permissions: [] },
        { key: 'admin', label: 'Stash admin', description: '', group: 'stash-admin', assignable: true, scope: 'global', permissions: [], privileged: true },
      ],
    },
  ],
};
const bundles = {
  items: [{ id: 'b1', name: 'Standard volunteer', description: '', roles: [{ role_name: 'stash:volunteer' }], members: 0, created_at: '', updated_at: '' }],
};

describe('AccessPicker', () => {
  beforeEach(() => {
    setRoutes({ 'GET /api/access-catalog': catalog, 'GET /api/access-bundles': bundles, 'GET /api/me': me(['access.manage']) });
  });

  it('saves picked roles and access groups', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    renderWithQuery(<AccessPicker plan={[]} editable onSave={onSave} />);
    fireEvent.click(await screen.findByText('Standard volunteer'));
    fireEvent.click(screen.getByText('Needs access to Stash'));
    // The first "Volunteer" is the access group's summary, the last the role card.
    fireEvent.click(screen.getAllByText('Volunteer').at(-1)!);
    fireEvent.click(screen.getByRole('button', { name: 'Save access plan' }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith([{ role_name: 'stash:volunteer', scope: 'global' }], ['b1']));
  });

  it('locks admin roles for users without the "assign admin roles" permission', async () => {
    renderWithQuery(<AccessPicker plan={[]} editable onSave={vi.fn()} />);
    fireEvent.click(await screen.findByText('Needs access to Stash'));
    expect(screen.getByText('Admin role: only HR admins can assign it')).toBeInTheDocument();
    expect(screen.getByText('Stash admin').closest('button')).toBeDisabled();
  });

  it('lets HR admins assign admin roles', async () => {
    setRoutes({ 'GET /api/access-catalog': catalog, 'GET /api/access-bundles': bundles, 'GET /api/me': me(['access.manage', 'access.assign_privileged']) });
    renderWithQuery(<AccessPicker plan={[]} editable onSave={vi.fn()} />);
    fireEvent.click(await screen.findByText('Needs access to Stash'));
    await waitFor(() => expect(screen.getByText('Stash admin').closest('button')).toBeEnabled());
  });

  it('shows roles the module no longer offers as retired', async () => {
    renderWithQuery(<AccessPicker plan={[{ role_name: 'stash:old', retired: true }]} editable onSave={vi.fn()} />);
    expect(await screen.findByText('stash:old')).toBeInTheDocument();
    expect(screen.getByText('retired')).toBeInTheDocument();
  });
});
