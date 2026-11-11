import { useState } from 'react';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, Archive, ChevronDown, Crown, RotateCcw, Users, X } from 'lucide-react';

import { listBundles } from '@/api/admin';
import { ApiError } from '@/api/client';
import {
  archiveDepartment,
  createDepartment,
  listAllDepartments,
  setDepartmentLeads,
  updateDepartment,
  type Department,
} from '@/api/departments';
import { canGlobal, getMe } from '@/api/me';
import { listPeople } from '@/api/people';
import { AccessPicker } from '@/components/AccessPicker';
import { SearchInput } from '@/components/SearchInput';
import { matches } from '@/lib/search';
import { useDebugMode } from '@/lib/debug';
import { noAutofill } from '@/lib/noAutofill';
import { useRoleLabels } from '@/lib/roles';
import { cn } from '@/lib/utils';

const inputCls = 'w-full rounded-md border border-input bg-background px-3 py-2 text-sm';

export function DepartmentsPage() {
  const departments = useQuery({ queryKey: ['departments', 'all'], queryFn: listAllDepartments });
  const me = useQuery({ queryKey: ['me'], queryFn: getMe });
  const canManage = canGlobal(me.data, 'departments.manage');
  const [editing, setEditing] = useState<Department | 'new' | null>(null);
  const [q, setQ] = useState('');
  const [showArchived, setShowArchived] = useState(false);
  const roles = useRoleLabels();
  const all = (departments.data?.items ?? []).filter((d) => showArchived || !d.archived_at);
  const shown = all.filter((d) =>
    matches(q, d.name, d.description, d.group_name, ...d.leads.map((l) => l.display_name),
      ...d.default_roles.flatMap((r) => [r.role_name, roles.text(r.role_name)])),
  );
  const archivedCount = (departments.data?.items ?? []).filter((d) => d.archived_at).length;

  return (
    <div className="p-6 space-y-6 max-w-5xl">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold">Departments</h1>
          <p className="text-sm text-muted-foreground">
            Group people into teams. New members of a department automatically get the access that department needs; its
            leads manage the people in it.
          </p>
        </div>
        {!editing && canManage && (
          <button onClick={() => setEditing('new')}
            className="shrink-0 whitespace-nowrap rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground">
            New department
          </button>
        )}
      </div>

      {editing && (
        <DepartmentEditor key={editing === 'new' ? 'new' : editing.id} dept={editing === 'new' ? undefined : editing}
          onDone={() => setEditing(null)} />
      )}

      <div className="flex flex-wrap items-center gap-3">
        {all.length > 0 && <SearchInput value={q} onChange={setQ} placeholder="Search departments, leads or access…" className="max-w-sm" />}
        {archivedCount > 0 && (
          <label className="flex items-center gap-2 text-sm text-muted-foreground">
            <input type="checkbox" checked={showArchived} onChange={(e) => setShowArchived(e.target.checked)} />
            Show {archivedCount} archived
          </label>
        )}
      </div>

      <div className="grid items-start gap-3 md:grid-cols-2">
        {shown.map((d) => <DepartmentCard key={d.id} d={d} canManage={canManage} onEdit={() => setEditing(d)} />)}
        {departments.data && all.length === 0 && (
          <p className="text-sm text-muted-foreground">No departments yet. Create one to start onboarding volunteers.</p>
        )}
        {all.length > 0 && shown.length === 0 && <p className="text-sm text-muted-foreground">No department matches.</p>}
      </div>
    </div>
  );
}

function DepartmentCard({ d, canManage, onEdit }: { d: Department; canManage: boolean; onEdit: () => void }) {
  const qc = useQueryClient();
  const debug = useDebugMode();
  const roles = useRoleLabels();
  const bundles = useQuery({ queryKey: ['access-bundles'], queryFn: listBundles });
  const [leadsOpen, setLeadsOpen] = useState(false);
  const retry = useMutation({
    mutationFn: () => updateDepartment(d.id, { name: d.name, description: d.description, default_roles: d.default_roles, default_bundles: d.default_bundles }),
    onSettled: () => qc.invalidateQueries({ queryKey: ['departments'] }),
  });
  const archive = useMutation({
    mutationFn: () => archiveDepartment(d.id, !d.archived_at),
    onSettled: () => qc.invalidateQueries({ queryKey: ['departments'] }),
  });
  const bundleName = new Map((bundles.data?.items ?? []).map((b) => [b.id, b.name]));

  return (
    <section className={cn('rounded-lg border border-border bg-card p-4 space-y-2', d.archived_at && 'opacity-70')}>
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 className="font-semibold">
            {d.name}
            {d.archived_at && <span className="ml-2 rounded-pill bg-muted px-2 py-0.5 text-xs font-normal">Archived</span>}
          </h2>
          {d.description && <p className="text-xs text-muted-foreground">{d.description}</p>}
        </div>
        {canManage && (
          <span className="flex shrink-0 gap-3 text-xs">
            {!d.archived_at && <button onClick={onEdit} className="text-primary hover:underline">Edit</button>}
            <button onClick={() => archive.mutate()} disabled={archive.isPending}
              title={d.archived_at ? 'Allow onboardings again' : 'No new onboardings; members keep their access'}
              className="inline-flex items-center gap-1 text-muted-foreground hover:text-foreground">
              {d.archived_at ? <><RotateCcw className="h-3 w-3" /> Restore</> : <><Archive className="h-3 w-3" /> Archive</>}
            </button>
          </span>
        )}
      </div>
      <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <Users className="h-3.5 w-3.5" /> {d.member_count} {d.member_count === 1 ? 'person' : 'people'}
      </p>
      <div className="flex flex-wrap items-center gap-1.5 text-xs">
        <Crown className="h-3.5 w-3.5 text-muted-foreground" />
        {d.leads.length === 0 && <span className="text-muted-foreground">No leads</span>}
        {d.leads.map((l) => <span key={l.person_id} className="rounded-pill bg-primary-soft px-2 py-0.5 text-primary">{l.display_name}</span>)}
        {canManage && !d.archived_at && (
          <button onClick={() => setLeadsOpen((o) => !o)} className="text-primary hover:underline">{leadsOpen ? 'Close' : 'Change leads'}</button>
        )}
      </div>
      {leadsOpen && <LeadsEditor d={d} onDone={() => setLeadsOpen(false)} />}
      <div className="flex flex-wrap gap-1.5">
        {d.default_roles.length === 0 && d.default_bundles.length === 0 && <span className="text-xs text-muted-foreground">No default access</span>}
        {d.default_bundles.map((id) => (
          <span key={id} className="rounded-pill border border-primary/40 bg-primary-soft px-2 py-0.5 text-xs">{bundleName.get(id) ?? 'Access group'}</span>
        ))}
        {d.default_roles.map((r) => (
          <span key={r.role_name} className="rounded-pill border border-border bg-muted px-2 py-0.5 text-xs" title={debug ? r.role_name : undefined}>
            {roles.text(r.role_name)}
          </span>
        ))}
      </div>
      {d.idp_sync_error && (
        <div className="flex flex-wrap items-center gap-2 text-xs text-destructive">
          <AlertTriangle className="h-3.5 w-3.5" />
          <span>Accounts for this department couldn&apos;t be updated.</span>
          {canManage && (
            <button type="button" onClick={() => retry.mutate()} disabled={retry.isPending} className="font-semibold underline">
              {retry.isPending ? 'Retrying…' : 'Retry'}
            </button>
          )}
          {debug && <span className="w-full font-mono text-[11px]">{d.idp_sync_error}</span>}
        </div>
      )}
      {debug && <p className="font-mono text-[11px] text-muted-foreground">groups {d.group_name}, {d.lead_group}</p>}
    </section>
  );
}

/** Picks the people on a department's lead list (they get the lead groups). */
function LeadsEditor({ d, onDone }: { d: Department; onDone: () => void }) {
  const qc = useQueryClient();
  const [leads, setLeads] = useState(d.leads);
  const [q, setQ] = useState('');
  const found = useQuery({
    queryKey: ['people', 'lead-search', q],
    queryFn: () => listPeople({ q, limit: 8 }),
    enabled: q.trim().length >= 2,
    placeholderData: keepPreviousData,
  });
  const save = useMutation({
    mutationFn: () => setDepartmentLeads(d.id, leads.map((l) => l.person_id)),
    onSuccess: async (res) => {
      await qc.invalidateQueries({ queryKey: ['departments'] });
      if (res.warnings.length === 0) onDone();
    },
  });
  return (
    <div className="space-y-2 rounded-md border border-border p-3 text-sm">
      <div className="flex flex-wrap gap-1.5">
        {leads.map((l) => (
          <span key={l.person_id} className="inline-flex items-center gap-1 rounded-pill bg-primary-soft px-2 py-0.5 text-xs text-primary">
            {l.display_name}
            <button type="button" aria-label={`Remove ${l.display_name}`} onClick={() => setLeads(leads.filter((x) => x.person_id !== l.person_id))}>
              <X className="h-3 w-3" />
            </button>
          </span>
        ))}
      </div>
      <SearchInput value={q} onChange={setQ} placeholder="Add a lead: name or email…" />
      {(found.data?.items ?? []).filter((p) => !leads.some((l) => l.person_id === p.id)).map((p) => (
        <button key={p.id} type="button" onClick={() => { setLeads([...leads, { person_id: p.id, display_name: p.display_name }]); setQ(''); }}
          className="block w-full rounded px-2 py-1 text-left hover:bg-muted">
          {p.display_name} <span className="text-xs text-muted-foreground">{p.email}</span>
        </button>
      ))}
      <p className="text-xs text-muted-foreground">
        Leads get the groups <code className="font-mono">{d.lead_group}</code> and the HR lead role, and may onboard, change access,
        pause and review people in this department only.
      </p>
      {save.data?.warnings.map((w) => <p key={w} className="text-xs text-warning-foreground">{w}</p>)}
      {save.error && <p className="text-xs text-destructive">{(save.error as Error).message}</p>}
      <div className="flex gap-2">
        <button type="button" onClick={() => save.mutate()} disabled={save.isPending}
          className="rounded-pill bg-primary px-3 py-1 text-xs font-semibold text-primary-foreground disabled:opacity-60">
          {save.isPending ? 'Saving…' : 'Save leads'}
        </button>
        <button type="button" onClick={onDone} className="rounded-pill border border-border px-3 py-1 text-xs">Cancel</button>
      </div>
    </div>
  );
}

function DepartmentEditor({ dept, onDone }: { dept?: Department; onDone: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState(dept?.name ?? '');
  const [slug, setSlug] = useState(dept?.slug ?? '');
  const [description, setDescription] = useState(dept?.description ?? '');
  const [advanced, setAdvanced] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const details = error instanceof ApiError ? (error.details as Record<string, string> | undefined) : undefined;
  const autoSlug = name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');

  return (
    <section className="rounded-lg border border-border bg-card p-4 space-y-4">
      <h2 className="font-semibold">{dept ? `Edit ${dept.name}` : 'New department'}</h2>
      <div className="grid gap-3 md:grid-cols-2">
        <label className="block text-sm">
          <span className="mb-1 block font-medium">Name <span className="text-destructive">*</span></span>
          <input {...noAutofill} value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. Logistics" className={inputCls} />
          {details?.name && <span className="text-xs text-destructive">{details.name}</span>}
        </label>
        <label className="block text-sm">
          <span className="mb-1 block font-medium">Description</span>
          <input {...noAutofill} value={description} onChange={(e) => setDescription(e.target.value)} className={inputCls} />
        </label>
      </div>

      <div>
        <button type="button" onClick={() => setAdvanced((a) => !a)} aria-expanded={advanced}
          className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">
          <ChevronDown className={cn('h-3.5 w-3.5 transition-transform', advanced && 'rotate-180')} /> Advanced
        </button>
        {(advanced || details?.slug) && (
          <label className="mt-2 block max-w-sm text-sm">
            <span className="mb-1 block font-medium">Short name</span>
            <input {...noAutofill} value={dept ? dept.slug : slug} disabled={!!dept} onChange={(e) => setSlug(e.target.value)}
              placeholder={autoSlug || 'made from the name'} className={cn(inputCls, 'disabled:opacity-60')} />
            <span className="text-xs text-muted-foreground">
              Account groups <code className="font-mono">dept-{dept?.slug ?? (slug || autoSlug || '…')}</code> and{' '}
              <code className="font-mono">dept-{dept?.slug ?? (slug || autoSlug || '…')}-lead</code>
              {dept && '; fixed once created'}
            </span>
            {details?.slug && <span className="block text-xs text-destructive">{details.slug}</span>}
          </label>
        )}
      </div>

      <AccessPicker
        plan={dept?.default_roles}
        bundleIds={dept?.default_bundles}
        editable
        subject="Every new member"
        intro="What new members of this department get by default. You can still adjust it per person during onboarding."
        allowSaveWhenClean
        saveLabel={dept ? 'Save department' : 'Create department'}
        onSave={async (roles, bundleIds) => {
          setError(null);
          try {
            const body = { name, slug: slug || undefined, description, default_roles: roles, default_bundles: bundleIds };
            if (dept) await updateDepartment(dept.id, body);
            else await createDepartment(body);
            await qc.invalidateQueries({ queryKey: ['departments'] });
            onDone();
          } catch (e) {
            setError(e);
            throw e;
          }
        }}
      />
      <button onClick={onDone} className="rounded-pill border border-border px-4 py-2 text-sm">Cancel</button>
    </section>
  );
}
