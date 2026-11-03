import { useSyncExternalStore } from 'react';
import { Moon, Sun } from 'lucide-react';

import { applyTheme, getAppliedTheme } from '@/lib/theme';
import { cn } from '@/lib/utils';

function subscribe(onStoreChange: () => void) {
  const obs = new MutationObserver(onStoreChange);
  obs.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
  return () => obs.disconnect();
}

export function ThemeToggle({ className }: { className?: string }) {
  const mode = useSyncExternalStore(subscribe, getAppliedTheme, () => 'light' as const);
  return (
    <button
      type="button"
      onClick={() => applyTheme(mode === 'dark' ? 'light' : 'dark')}
      className={cn(
        'flex items-center gap-2.5 rounded-pill px-3 py-2 text-sm font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground',
        className,
      )}
      aria-label={mode === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}
    >
      {mode === 'dark' ? <Sun className="h-4 w-4 shrink-0" /> : <Moon className="h-4 w-4 shrink-0" />}
      {mode === 'dark' ? 'Light mode' : 'Dark mode'}
    </button>
  );
}
