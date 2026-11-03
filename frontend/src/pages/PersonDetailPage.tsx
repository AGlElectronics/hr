import { useState, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams, useSearch } from '@tanstack/react-router';
import { Pencil } from 'lucide-react';

import { getPerson, SOCIAL_NETWORKS, updatePerson, type PersonInput } from '@/api/people';
import { listDrafts } from '@/api/drafts';
import { PersonActions, useInvalidatePerson } from '@/components/PersonActions';
import { PersonForm } from '@/components/PersonForm';
import { PersonStatusBadge } from '@/components/PersonStatusBadge';

export function PersonDetailPage() {
  const { id } = useParams({ from: '/app/people/$id' });
  const search = useSearch({ from: '/app/people/$id' });
  const navigate = useNavigate();
  const qc = useQueryClient();
  const invalidate = useInvalidatePerson(id);
  const editing = !!search.edit;
  const setEditing = (on: boolean) =>
    void navigate({ to: '/people/$id', params: { id }, search: on ? { edit: true } : {}, replace: true });
  const [syncWarning, setSyncWarning] = useState<string | null>(null);

  const personQuery = useQuery({ queryKey: ['person', id], queryFn: () => getPerson(id) });
  const draftsQuery = useQuery({ queryKey: ['drafts'], queryFn: () => listDrafts() });

  const updateMutation = useMutation({
    mutationFn: (input: PersonInput) => updatePerson(id, input),
    onSuccess: (p) => {
      qc.setQueryData(['person', id], p);
      invalidate();
      setSyncWarning(p.idp_sync_error ?? null);
      setEditing(false);
    },
  });

  if (personQuery.isLoading) return <p className="p-6 text-muted-foreground">Loading…</p>;
  if (personQuery.error || !personQuery.data) {
    return <p className="p-6 text-destructive">{(personQuery.error as Error)?.message ?? 'Not found'}</p>;
  }
  const p = personQuery.data;
  const drafts = (draftsQuery.data?.items ?? []).filter((d) => d.person_id === p.id);
  const socials = SOCIAL_NETWORKS.filter((n) => p.social_handles?.[n.key]);
  const ec = p.emergency_contact ?? {};

  return (
    <div className="p-6 max-w-5xl space-y-6">
      <Link to="/people" className="text-sm text-muted-foreground hover:underline">← People</Link>
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold">{p.display_name}</h1>
          <p className="text-sm text-muted-foreground">
            {p.legal_name} · <PersonStatusBadge status={p.status} />
          </p>
        </div>
        {!editing && (
          <button onClick={() => setEditing(true)}
            className="inline-flex items-center gap-2 rounded-pill bg-primary px-5 py-2 text-sm font-semibold text-primary-foreground">
            <Pencil className="h-4 w-4" /> Edit profile
          </button>
        )}
      </div>

      {syncWarning && (
        <p className="rounded-md border border-warning/40 bg-warning-soft px-4 py-2 text-sm text-warning-foreground">
          Profile saved, but Keycloak could not be updated: {syncWarning}
        </p>
      )}
      {p.email_missing && !editing && (
        <p className="rounded-md border border-warning/40 bg-warning-soft px-4 py-2 text-sm text-warning-foreground">
          This person has no email yet. Add one before onboarding — it becomes their login username.
        </p>
      )}

      {editing ? (
        <section className="rounded-lg border-2 border-primary/40 bg-card p-4">
          <h2 className="mb-3 font-semibold">Editing profile</h2>
          <PersonForm
            initial={p}
            statusReadOnly
            submitLabel="Save changes"
            pending={updateMutation.isPending}
            error={updateMutation.error}
            onSubmit={(input) => updateMutation.mutate(input)}
            onCancel={() => {
              updateMutation.reset();
              setEditing(false);
            }}
          />
        </section>
      ) : (
        <div className="grid gap-6 lg:grid-cols-[1fr_22rem]">
          <div className="grid content-start gap-4 md:grid-cols-2">
            <Card title="Contact" onEdit={() => setEditing(true)}>
              <Row label="Private email" value={p.email_missing ? undefined : p.email} />
              <Row label="Organization email" value={p.org_email} />
              <Row label="Phone" value={p.phone} />
              <Row label="Address" value={p.address} />
            </Card>
            <Card title="Identity" onEdit={() => setEditing(true)}>
              <Row label="First name" value={p.first_name} />
              <Row label="Last name" value={p.last_name} />
              <Row label="Nickname" value={p.nickname} />
              <Row label="Date of birth" value={p.date_of_birth} />
              {p.is_banned && <Row label="Flag" value="Banned" />}
            </Card>
            <Card title="Emergency contact" onEdit={() => setEditing(true)}>
              <Row label="Name" value={ec.name} />
              <Row label="Phone" value={ec.phone} />
              <Row label="Email" value={ec.email} />
              <Row label="Comments" value={ec.comments} />
            </Card>
            <Card title="Social handles" onEdit={() => setEditing(true)}>
              {socials.length === 0 && <p className="text-sm text-muted-foreground">None recorded.</p>}
              {socials.map((n) => (
                <Row key={n.key} label={n.label} value={p.social_handles[n.key]} />
              ))}
            </Card>
            <Card title="Notes" onEdit={() => setEditing(true)}>
              <p className="whitespace-pre-wrap text-sm">{p.notes || <span className="text-muted-foreground">—</span>}</p>
            </Card>
          </div>

          <aside className="space-y-4">
            <section className="rounded-lg border border-border bg-card p-4">
              <PersonActions personId={p.id} />
            </section>
            <section className="rounded-lg border border-border bg-card">
              <header className="border-b border-border px-4 py-3 text-sm font-semibold">Onboarding history</header>
              <ul className="divide-y divide-border">
                {drafts.map((d) => (
                  <li key={d.id} className="flex items-center justify-between px-4 py-2 text-sm">
                    {d.department_id ? (
                      <Link to="/onboarding/$id/review" params={{ id: d.id }} className="font-medium hover:underline">{d.title}</Link>
                    ) : (
                      <Link to="/drafts/$id" params={{ id: d.id }} className="font-medium hover:underline">{d.title}</Link>
                    )}
                    <span className="text-xs text-muted-foreground">{d.state.replace('_', ' ')}</span>
                  </li>
                ))}
                {drafts.length === 0 && <li className="px-4 py-4 text-sm text-muted-foreground">None yet.</li>}
              </ul>
            </section>
          </aside>
        </div>
      )}
    </div>
  );
}

function Card({ title, children, onEdit }: { title: string; children: ReactNode; onEdit: () => void }) {
  return (
    <section className="group rounded-lg border border-border bg-card p-4 space-y-2">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold">{title}</h2>
        <button type="button" onClick={onEdit} aria-label={`Edit ${title}`}
          className="inline-flex items-center gap-1 text-xs text-primary opacity-70 hover:underline group-hover:opacity-100">
          <Pencil className="h-3 w-3" /> Edit
        </button>
      </div>
      {children}
    </section>
  );
}

function Row({ label, value }: { label: string; value?: string }) {
  return (
    <div className="grid grid-cols-[9rem_1fr] gap-2 text-sm">
      <span className="text-muted-foreground">{label}</span>
      <span className="break-words">{value || '—'}</span>
    </div>
  );
}
