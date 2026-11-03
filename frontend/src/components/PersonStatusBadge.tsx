import { PERSON_STATUSES, type PersonStatus } from '@/api/people';

const tone: Record<PersonStatus, string> = {
  applicant: 'bg-slate-100 text-slate-700',
  onboarding: 'bg-amber-100 text-amber-800',
  active: 'bg-emerald-100 text-emerald-800',
  paused: 'bg-sky-100 text-sky-800',
  offboarded: 'bg-zinc-200 text-zinc-600',
};

export function PersonStatusBadge({ status }: { status: PersonStatus }) {
  const label = PERSON_STATUSES.find((s) => s.value === status)?.label ?? status;
  return (
    <span className={`inline-block rounded-full px-2 py-0.5 text-[11px] font-semibold ${tone[status] ?? tone.applicant}`}>
      {label}
    </span>
  );
}
