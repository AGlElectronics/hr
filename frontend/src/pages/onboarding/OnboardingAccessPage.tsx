import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from '@tanstack/react-router';

import { assignRoles } from '@/api/drafts';
import { getOnboarding } from '@/api/onboarding';
import { AccessPicker } from '@/components/AccessPicker';
import { OnboardingSteps } from '@/components/OnboardingSteps';
import { useDebugMode } from '@/lib/debug';

/** Step 2: module access, prefilled with the department defaults. */
export function OnboardingAccessPage() {
  const { id } = useParams({ from: '/app/onboarding/$id/access' });
  const navigate = useNavigate();
  const qc = useQueryClient();
  const view = useQuery({ queryKey: ['onboarding', id], queryFn: () => getOnboarding(id) });
  const debug = useDebugMode();

  if (view.isLoading) return <p className="p-6 text-muted-foreground">Loading…</p>;
  if (view.error || !view.data) return <p className="p-6 text-destructive">{(view.error as Error)?.message}</p>;
  const { draft, person, department } = view.data;
  const editable = draft.state === 'draft';

  return (
    <div className="p-6 max-w-4xl space-y-5">
      <div>
        <h1 className="text-2xl font-bold">Access for {person.display_name}</h1>
        <p className="text-sm text-muted-foreground">
          {department ? (
            <>
              As part of <strong>{department.name}</strong>, they already get the department&apos;s usual access, ticked
              below. Tick or untick anything they need.
              {debug && <span className="ml-1 font-mono text-xs">(group {department.group_name})</span>}
            </>
          ) : (
            <>
              No department picked yet —{' '}
              <Link to="/onboarding/$id/details" params={{ id }} className="text-primary hover:underline">go back</Link>.
            </>
          )}
        </p>
      </div>
      <OnboardingSteps current="access" id={id} locked={!editable} />
      {!editable && (
        <p className="rounded-md border border-border bg-muted/60 px-4 py-2 text-sm">
          This onboarding is completed, so its access is read-only.{' '}
          <Link to="/onboarding/$id/review" params={{ id }} className="text-primary hover:underline">Back to overview</Link>
        </p>
      )}
      <AccessPicker
        plan={draft.roles}
        editable={editable}
        defaults={department?.default_roles.map((r) => r.role_name)}
        intro={<>Switch on the modules they need, then tick what they should be able to do.</>}
        allowSaveWhenClean
        saveLabel="Save & continue to review"
        onSave={async (roles) => {
          if (editable) await assignRoles(id, { roles });
          await qc.invalidateQueries({ queryKey: ['onboarding', id] });
          await navigate({ to: '/onboarding/$id/review', params: { id } });
        }}
      />
    </div>
  );
}
