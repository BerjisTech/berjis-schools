import { CanActivateFn } from '@angular/router';
import { loginUrl, verifySession } from '../util';

export const authGuard: CanActivateFn = async (_route, _state) => {
  const result = await verifySession({ attemptRefresh: true });
  const ok = !!result.valid;
  if (ok) return true;
  // Redirect to centralized login with return URL
  window.location.href = loginUrl();
  return false;
};
