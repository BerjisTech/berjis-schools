import { CanActivateFn } from '@angular/router';
import { hasAppRole, loginUrl } from '../util';

// Only allow users who have the schools.owner role
export const schoolsOwnerGuard: CanActivateFn = async (_route, _state) => {
  void _route; void _state;
  try {
    const ok = await hasAppRole('schools', 'owner');
    if (ok) return true;
  } catch {}
  // If not owner, push them to create flow (after auth)
  window.location.href = loginUrl('/schools/create');
  return false;
};

