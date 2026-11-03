import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from '@tanstack/react-router';

import '@/auth/captureCallbackUrl';
import '@fontsource-variable/inter';
import './styles.css';
import { router } from '@/router';
import { queryClient } from '@/lib/queryClient';
import { installAuth } from '@/auth/installAuth';

installAuth();

const rootEl = document.getElementById('root');
if (!rootEl) {
  throw new Error('#root element missing from index.html');
}

createRoot(rootEl).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
