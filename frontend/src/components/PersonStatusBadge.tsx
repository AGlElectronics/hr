import { PERSON_STATUSES, type PersonStatus } from '@/api/people';

const tone: Record<PersonStatus, string> = {
  applicant: 'bg-primary-soft text-primary',
  onboarding: 'bg-warning-soft text-warning-foreground',
  active: 'bg-success-soft text-success-foreground',
  paused: 'bg-info-soft text-info-foreground',
};

export function PersonStatusBadge({ status }: { status: PersonStatus }) {
  const label = PERSON_STATUSES.find((s) => s.value === status)?.label ?? status;
  return (
    <span className={`inline-block rounded-full px-2 py-0.5 text-[11px] font-semibold ${tone[status] ?? tone.applicant}`}>
      {label}
    </span>
  );
}
