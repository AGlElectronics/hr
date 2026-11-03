import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, CheckCircle2 } from 'lucide-react';

import { ApiError } from '@/api/client';
import { createDepartment, listDepartments, updateDepartment, type Department } from '@/api/departments';
import { AccessPicker } from '@/components/AccessPicker';

export function DepartmentsPage() {
  const departments = useQuery({ queryKey: ['departments'], queryFn: listDepartments });
  const [editing, setEditing] = useState<Department | 'new' | null>(null);

  return (
    <div className="p-6 space-y-6 max-w-5xl">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold">Departments</h1>
          <p className="text-sm text-muted-foreground">
            Each department gets its own Keycloak group (created automatically on save) and a default set of module
            roles that new volunteers start with during onboarding.
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

      <div className="grid gap-3 md:grid-cols-2">
        {(departments.data?.items ?? []).map((d) => (
          <section key={d.id} className="rounded-lg border border-border bg-card p-4 space-y-2">
            <div className="flex items-start justify-between gap-2">
              <div>
                <h2 className="font-semibold">{d.name}</h2>
                {d.description && <p className="text-xs text-muted-foreground">{d.description}</p>}
              </div>
              <button onClick={() => setEditing(d)} className="text-xs text-primary hover:underline">Edit</button>
            </div>
            <p className="flex items-center gap-1.5 text-xs">
              {d.idp_sync_error ? (
                <>
                  <AlertTriangle className="h-3.5 w-3.5 text-destructive" />
                  <span className="text-destructive" title={d.idp_sync_error}>Keycloak group not synced — save again to retry</span>
                </>
              ) : (
                <>
                  <CheckCircle2 className="h-3.5 w-3.5 text-emerald-600" />
                  Keycloak group <code className="font-mono">{d.group_name}</code>
                </>
              )}
            </p>
            <div className="flex flex-wrap gap-1.5">
              {d.default_roles.length === 0 && <span className="text-xs text-muted-foreground">No default access</span>}
              {d.default_roles.map((r) => (
                <span key={r.role_name} className="rounded-pill border border-border bg-muted px-2 py-0.5 text-xs">
                  {r.role_name}
                </span>
              ))}
            </div>
          </section>
        ))}
        {departments.data?.items.length === 0 && (
          <p className="text-sm text-muted-foreground">No departments yet. Create one to start onboarding volunteers.</p>
        )}
      </div>
    </div>
  );
}

function DepartmentEditor({ dept, onDone }: { dept?: Department; onDone: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState(dept?.name ?? '');
  const [slug, setSlug] = useState(dept?.slug ?? '');
  const [description, setDescription] = useState(dept?.description ?? '');
  const [error, setError] = useState<unknown>(null);
  const details = error instanceof ApiError ? (error.details as Record<string, string> | undefined) : undefined;
  const autoSlug = name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');

  return (
    <section className="rounded-lg border-2 border-primary/40 bg-card p-4 space-y-4">
      <h2 className="font-semibold">{dept ? `Edit ${dept.name}` : 'New department'}</h2>
      <div className="grid gap-3 md:grid-cols-3">
        <label className="block text-sm">
          <span className="mb-1 block font-medium">Name <span className="text-destructive">*</span></span>
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. Con Ops"
            className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm" />
          {details?.name && <span className="text-xs text-destructive">{details.name}</span>}
        </label>
        <label className="block text-sm">
          <span className="mb-1 block font-medium">Slug</span>
          <input value={dept ? dept.slug : slug} disabled={!!dept} onChange={(e) => setSlug(e.target.value)}
            placeholder={autoSlug || 'auto from name'}
            className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm disabled:opacity-60" />
          <span className="text-xs text-muted-foreground">
            Keycloak group: <code className="font-mono">dept-{dept?.slug ?? (slug || autoSlug || '…')}</code>
            {dept && ' (fixed)'}
          </span>
          {details?.slug && <span className="block text-xs text-destructive">{details.slug}</span>}
        </label>
        <label className="block text-sm">
          <span className="mb-1 block font-medium">Description</span>
          <input value={description} onChange={(e) => setDescription(e.target.value)}
            className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm" />
        </label>
      </div>

      <AccessPicker
        plan={dept?.default_roles}
        editable
        subject="Every new member"
        intro="Default access for new members of this department. HR can still adjust it per person during onboarding."
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
