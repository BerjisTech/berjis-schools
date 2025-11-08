import { CanActivateFn } from '@angular/router';
import { hasAppRole, loginUrl, verifySession } from '../util';

export const schoolsModeratorGuard: CanActivateFn = async (_route, _state) => {
  void _route; void _state;
  const v = await verifySession({ attemptRefresh: true });
  if (!v.valid) { window.location.href = loginUrl(); return false; }
  try {
    const ok = await hasAppRole('schools', 'moderator');
    if (ok) return true;
  } catch {}
  window.location.href = '/';
  return false;
};

