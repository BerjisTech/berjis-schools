import { CanActivateFn } from '@angular/router';
import { loginUrl, verifySession } from '../util';

export const authGuard: CanActivateFn = async (_route, _state) => {
  // mark params as used to satisfy lint while keeping signature
  void _route; void _state;
  const result = await verifySession({ attemptRefresh: true });
  const ok = !!result.valid;
  if (ok) return true;
  // Redirect to centralized login with return URL
  window.location.href = loginUrl();
  return false;
};
