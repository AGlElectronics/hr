import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, ChevronDown, Users } from 'lucide-react';

import { ApiError } from '@/api/client';
import { createDepartment, listDepartments, updateDepartment, type Department } from '@/api/departments';
import { AccessPicker } from '@/components/AccessPicker';
import { SearchInput } from '@/components/SearchInput';
import { matches } from '@/lib/search';
import { useDebugMode } from '@/lib/debug';
import { noAutofill } from '@/lib/noAutofill';
import { useRoleLabels } from '@/lib/roles';
import { cn } from '@/lib/utils';

const inputCls = 'w-full rounded-md border border-input bg-background px-3 py-2 text-sm';

export function DepartmentsPage() {
  const departments = useQuery({ queryKey: ['departments'], queryFn: listDepartments });
  const [editing, setEditing] = useState<Department | 'new' | null>(null);
  const [q, setQ] = useState('');
  const roles = useRoleLabels();
  const all = departments.data?.items ?? [];
  const shown = all.filter((d) =>
    matches(q, d.name, d.description, d.group_name, ...d.default_roles.flatMap((r) => [r.role_name, roles.text(r.role_name)])),
  );

  return (
    <div className="p-6 space-y-6 max-w-5xl">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold">Departments</h1>
          <p className="text-sm text-muted-foreground">
            Group people into teams. New members of a department automatically get the access that department needs.
          </p>
        </div>
        {!editing && (
          <button onClick={() => setEditing('new')}
            className="shrink-0 whitespace-nowrap rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground">
            New department
          </button>
        )}
      </div>

      {editing && (
        <DepartmentEditor
          key={editing === 'new' ? 'new' : editing.id}
          dept={editing === 'new' ? undefined : editing}
          onDone={() => setEditing(null)}
        />
      )}

      {all.length > 0 && <SearchInput value={q} onChange={setQ} placeholder="Search departments or access…" className="max-w-sm" />}

      <div className="grid items-start gap-3 md:grid-cols-2">
        {shown.map((d) => <DepartmentCard key={d.id} d={d} onEdit={() => setEditing(d)} />)}
        {departments.data && all.length === 0 && (
          <p className="text-sm text-muted-foreground">No departments yet. Create one to start onboarding volunteers.</p>
        )}
        {all.length > 0 && shown.length === 0 && <p className="text-sm text-muted-foreground">No department matches.</p>}
      </div>
    </div>
  );
}

function DepartmentCard({ d, onEdit }: { d: Department; onEdit: () => void }) {
  const qc = useQueryClient();
  const debug = useDebugMode();
  const roles = useRoleLabels();
  const retry = useMutation({
    mutationFn: () => updateDepartment(d.id, { name: d.name, description: d.description, default_roles: d.default_roles }),
    onSettled: () => qc.invalidateQueries({ queryKey: ['departments'] }),
  });

  return (
    <section className="rounded-lg border border-border bg-card p-4 space-y-2">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 className="font-semibold">{d.name}</h2>
          {d.description && <p className="text-xs text-muted-foreground">{d.description}</p>}
        </div>
        <button onClick={onEdit} className="text-xs text-primary hover:underline">Edit</button>
      </div>
      <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <Users className="h-3.5 w-3.5" /> {d.member_count} {d.member_count === 1 ? 'person' : 'people'}
      </p>
      <div className="flex flex-wrap gap-1.5">
        {d.default_roles.length === 0 && <span className="text-xs text-muted-foreground">No default access</span>}
        {d.default_roles.map((r) => (
          <span key={r.role_name} className="rounded-pill border border-border bg-muted px-2 py-0.5 text-xs"
            title={debug ? r.role_name : undefined}>
            {roles.text(r.role_name)}
          </span>
        ))}
      </div>
      {d.idp_sync_error && (
        <div className="flex flex-wrap items-center gap-2 text-xs text-destructive">
          <AlertTriangle className="h-3.5 w-3.5" />
          <span>Accounts for this department couldn&apos;t be updated.</span>
          <button type="button" onClick={() => retry.mutate()} disabled={retry.isPending} className="font-semibold underline">
            {retry.isPending ? 'Retrying…' : 'Retry'}
          </button>
          {debug && <span className="w-full font-mono text-[11px]">{d.idp_sync_error}</span>}
        </div>
      )}
      {debug && <p className="font-mono text-[11px] text-muted-foreground">group {d.group_name}</p>}
    </section>
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
              Account group <code className="font-mono">dept-{dept?.slug ?? (slug || autoSlug || '…')}</code>
              {dept && '; fixed once created'}
            </span>
            {details?.slug && <span className="block text-xs text-destructive">{details.slug}</span>}
          </label>
        )}
      </div>

      <AccessPicker
        plan={dept?.default_roles}
        editable
        subject="Every new member"
        intro="What new members of this department get by default. You can still adjust it per person during onboarding."
        allowSaveWhenClean
        saveLabel={dept ? 'Save department' : 'Create department'}
        onSave={async (roles) => {
          setError(null);
          try {
            const body = { name, slug: slug || undefined, description, default_roles: roles };
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
