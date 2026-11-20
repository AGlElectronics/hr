import { fireEvent, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { calls, fakeApiRequest, renderWithQuery, routerMock, setRoutes } from '@/test/helpers';

vi.mock('@/api/client', async (orig) => ({ ...(await orig<object>()), apiRequest: fakeApiRequest }));
vi.mock('@tanstack/react-router', () => routerMock);

const { OnboardingReviewPage } = await import('./OnboardingReviewPage');

const person = {
  id: 'p1',
  first_name: 'Paula',
  last_name: 'Example',
  nickname: '',
  legal_name: 'Paula Example',
  display_name: 'Paula Example',
  username: 'paula.example',
  email: 'paula@example.org',
  phone: '+31612345678',
  address: { street: 'Main 1', postal_code: '1000 AA', city: 'Amsterdam', country: 'NL' },
  social_handles: {},
  emergency_contact: { name: 'Pat', phone: '+31612345679' },
};

function view(state: string, provisioning?: object) {
  return {
    draft: { id: 'draft-1', person_id: 'p1', state, title: 'Security', roles: [], created_by: 'x', created_at: '', updated_at: '' },
    person,
    department: { id: 'd1', name: 'Security', group_name: 'dept-security' },
    missing_fields: {},
    provisioning,
  };
}

function routes(v: object, extra: Record<string, unknown> = {}) {
  setRoutes({
    'GET /api/onboardings/draft-1': v,
    'GET /api/onboardings/draft-1/documents': {
      items: [{ id: 'doc-1', template_id: 't1', template_name: 'NDA', required: true, status: 'pending' }],
    },
    'GET /api/access-catalog': { modules: [] },
    'GET /api/check/email': { valid: true, deliverable: true },
    ...extra,
  });
}

describe('OnboardingReviewPage', () => {
  it('sends for signing only after the check box is ticked', async () => {
    routes(view('draft'), {
      'POST /api/onboardings/draft-1/submit-for-signing': {
        draft: { id: 'draft-1', state: 'pending_approval' },
        sent: 1,
        message: 'Sign links sent.',
      },
    });
    renderWithQuery(<OnboardingReviewPage />);
    const send = await screen.findByRole('button', { name: 'Send for signing' });
    expect(send).toBeDisabled();
    fireEvent.click(screen.getByRole('checkbox'));
    fireEvent.click(send);
    await waitFor(() => expect(calls.some((c) => c.key === 'POST /api/onboardings/draft-1/submit-for-signing')).toBe(true));
  });

  it('waits for signatures without offering Approve', async () => {
    setRoutes({
      'GET /api/onboardings/draft-1': view('pending_approval'),
      'GET /api/onboardings/draft-1/documents': {
        items: [{ id: 'doc-1', draft_id: 'draft-1', template_id: 't1', template_name: 'NDA', required: true, status: 'sent', created_at: '', updated_at: '' }],
      },
      'GET /api/access-catalog': { modules: [] },
      'GET /api/check/email': { valid: true, deliverable: true },
    });
    renderWithQuery(<OnboardingReviewPage />);
    expect(await screen.findByRole('button', { name: 'Resend sign links' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Approve & create account' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Send for signing' })).not.toBeInTheDocument();
  });

  it('approves only after signatures and the human check box', async () => {
    routes(view('pending_approval'), {
      'GET /api/onboardings/draft-1/documents': {
        items: [{ id: 'doc-1', template_id: 't1', template_name: 'NDA', required: true, status: 'signed' }],
      },
      'POST /api/onboardings/draft-1/complete': {
        ok: true,
        queued: true,
        jobs: [],
        groups_applied: null,
        account_created: false,
        password_setup_email_sent: false,
      },
    });
    renderWithQuery(<OnboardingReviewPage />);
    const approve = await screen.findByRole('button', { name: 'Approve & create account' });
    expect(approve).toBeDisabled();
    fireEvent.click(screen.getByRole('checkbox'));
    fireEvent.click(approve);
    await waitFor(() => expect(calls.some((c) => c.key === 'POST /api/onboardings/draft-1/complete')).toBe(true));
  });

  it('follows the background job while the account is set up', async () => {
    routes(view('provisioning', { job_id: 'j1', status: 'running', attempts: 2, max_attempts: 5, error: 'timeout', queued_at: '' }), {
      'GET /api/onboardings/draft-1/documents': { items: [] },
    });
    renderWithQuery(<OnboardingReviewPage />);
    expect(await screen.findByText('Setting up their account…')).toBeInTheDocument();
    expect(screen.getByText(/Attempt 2 of 5/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Send for signing' })).not.toBeInTheDocument();
  });

  it('offers to resend the set-password email when it failed, and never shows a password', async () => {
    const result = {
      ok: true,
      jobs: [],
      groups_applied: ['dept-security'],
      account_created: true,
      password_setup_email_sent: false,
      password_setup_error: 'SMTP not configured',
    };
    routes(view('active', { job_id: 'j1', status: 'succeeded', attempts: 1, max_attempts: 5, result, queued_at: '' }), {
      'GET /api/onboardings/draft-1/documents': { items: [] },
      'POST /api/people/p1/password-setup-email': { sent: true },
    });
    renderWithQuery(<OnboardingReviewPage />);
    fireEvent.click(await screen.findByRole('button', { name: 'Resend set-password email' }));
    expect(await screen.findByRole('button', { name: 'Email sent' })).toBeInTheDocument();
    expect(screen.queryByText(/temporary password/i)).not.toBeInTheDocument();
  });
});
