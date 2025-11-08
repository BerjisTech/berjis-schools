import { CanActivateFn } from '@angular/router';
import { hasAppRole, hasAnySchoolRole, loginUrl, verifySession } from '../util';

// Allows independent private tutors OR any school tutor
export const schoolsTutorGuard: CanActivateFn = async (_route, _state) => {
  void _route; void _state;
  // Ensure session first (plays well when used after authGuard but safe standalone)
  const v = await verifySession({ attemptRefresh: true });
  if (!v.valid) { window.location.href = loginUrl(); return false; }
  try {
    const isPrivate = await hasAppRole('schools', 'private_tutor');
    if (isPrivate) return true;
  } catch {}
  try {
    const isSchoolTutor = await hasAnySchoolRole('tutor');
    if (isSchoolTutor) return true;
  } catch {}
  // Fall back: point them to tutor application flow
  window.location.href = '/tutors/become';
  return false;
};

