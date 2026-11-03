import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams, useSearch } from '@tanstack/react-router';

import { listDepartments } from '@/api/departments';
import { getOnboarding, startOnboarding, updateOnboardingDetails } from '@/api/onboarding';
import { getPerson, type Person } from '@/api/people';
import { OnboardingSteps } from '@/components/OnboardingSteps';
import { PersonForm } from '@/components/PersonForm';

/** Values the dashboard quick start can hand to a new onboarding. */
export type OnboardingPrefill = {
  person?: string;
  department?: string;
  first_name?: string;
  last_name?: string;
  email?: string;
};

/** Step 1: volunteer profile (every definition field) + department. */
export function OnboardingDetailsPage() {
  const { id } = useParams({ strict: false }) as { id?: string };
  const search = useSearch({ strict: false }) as OnboardingPrefill;

  const view = useQuery({ queryKey: ['onboarding', id], queryFn: () => getOnboarding(id!), enabled: !!id });
  const existingPerson = useQuery({
    queryKey: ['person', search.person],
    queryFn: () => getPerson(search.person!),
    enabled: !id && !!search.person,
  });

  if ((id && view.isLoading) || existingPerson.isLoading) {
    return <p className="p-6 text-muted-foreground">Loading…</p>;
  }
  if (view.error) return <p className="p-6 text-destructive">{(view.error as Error).message}</p>;

  const prefill =
    !id && !search.person && (search.first_name || search.last_name || search.email)
      ? ({
          first_name: search.first_name ?? '',
          last_name: search.last_name ?? '',
          email: search.email ?? '',
          email_missing: false,
          social_handles: {},
          emergency_contact: {},
        } as unknown as Person)
      : undefined;
  const person = id ? view.data?.person : (existingPerson.data ?? prefill);
  return (
    <DetailsForm
      key={id ?? search.person ?? `new-${search.first_name ?? ''}-${search.email ?? ''}`}
      id={id}
      person={person}
      personId={!id ? existingPerson.data?.id : undefined}
      departmentId={view.data?.draft.department_id ?? (!id ? search.department ?? '' : '')}
      locked={!!id && view.data?.draft.state !== 'draft'}
    />
  );
}

function DetailsForm({
  id,
  person,
  personId,
  departmentId: initialDept,
  locked,
}: {
  id?: string;
  person?: Person;
  personId?: string;
  departmentId: string;
  locked: boolean;
}) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const departments = useQuery({ queryKey: ['departments'], queryFn: listDepartments });
  const [departmentId, setDepartmentId] = useState(initialDept);
  const dept = departments.data?.items.find((d) => d.id === departmentId);

  const save = useMutation({
    mutationFn: (personInput: Parameters<typeof startOnboarding>[0]['person']) =>
      id
        ? updateOnboardingDetails(id, { person: personInput, department_id: departmentId })
        : startOnboarding({ person_id: personId, person: personInput, department_id: departmentId }),
    onSuccess: (v) => {
      qc.setQueryData(['onboarding', v.draft.id], v);
      void qc.invalidateQueries({ queryKey: ['people'] });
      void qc.invalidateQueries({ queryKey: ['drafts'] });
      void navigate({ to: '/onboarding/$id/access', params: { id: v.draft.id } });
    },
  });

  if (locked && id) {
    return (
      <div className="p-6 space-y-4">
        <OnboardingSteps current="details" id={id} locked />
        <p>This onboarding has already been completed.</p>
        <Link to="/onboarding/$id/review" params={{ id }} className="text-primary hover:underline">View overview</Link>
      </div>
    );
  }

  const deptSection = (
    <fieldset className="rounded-lg border-2 border-primary/40 p-4">
      <legend className="px-1 text-sm font-semibold">Department</legend>
      <p className="mb-3 text-xs text-muted-foreground">
        The department that approved this volunteer. They join its group and get its default access, which you can
        adjust on the next page.
      </p>
      {departments.data?.items.length === 0 ? (
        <p className="text-sm">
          No departments yet.{' '}
          <Link to="/departments" className="text-primary hover:underline">Create one first</Link>.
        </p>
      ) : (
        <div className="grid gap-3 md:grid-cols-2">
          <label className="block text-sm">
            <span className="mb-1 block font-medium">
              Department <span className="text-destructive">*</span>
            </span>
            <select
              required
              value={departmentId}
              onChange={(e) => setDepartmentId(e.target.value)}
              className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
            >
              <option value="">— choose —</option>
              {(departments.data?.items ?? []).map((d) => (
                <option key={d.id} value={d.id}>{d.name}</option>
              ))}
            </select>
            {id && initialDept && departmentId !== initialDept && (
              <span className="mt-1 block text-xs text-warning">
                Changing department resets the access plan to the new department&apos;s defaults.
              </span>
            )}
          </label>
          {dept && (
            <div className="rounded-md bg-muted/60 p-3 text-xs">
              {dept.description && <p className="mb-1">{dept.description}</p>}
              <p>
                Group <code className="font-mono">{dept.group_name}</code>
                {' · '}
                {dept.default_roles.length === 0
                  ? 'no default access'
                  : `default access: ${dept.default_roles.map((r) => r.role_name).join(', ')}`}
              </p>
            </div>
          )}
        </div>
      )}
    </fieldset>
  );

  return (
    <div className="p-6 max-w-4xl space-y-5">
      <div>
        <h1 className="text-2xl font-bold">{id ? 'Edit onboarding' : 'New onboarding'}</h1>
        <p className="text-sm text-muted-foreground">
          For a volunteer whose application was approved by their department. Every field marked{' '}
          <span className="text-destructive">*</span> is required.
        </p>
      </div>
      <OnboardingSteps current="details" id={id} />
      <PersonForm
        initial={person}
        hideStatus
        before={deptSection}
        canSubmitExtra={!!departmentId}
        submitLabel="Save & continue to access"
        pending={save.isPending}
        error={save.error}
        onSubmit={(input) => save.mutate(input)}
        onCancel={() => void navigate({ to: '/onboarding' })}
      />
    </div>
  );
}
