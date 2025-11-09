import { createAuthGuard } from '@berjis/angular-auth';

export const authGuard = createAuthGuard({
  ensureOptions: { maxAgeMs: 1500 }
});
