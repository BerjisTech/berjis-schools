import { CanActivateFn } from '@angular/router';
import { loginUrl } from '../util';

async function verifyAuth(): Promise<boolean> {
  try {
    const root = window.location.origin;
    // Core API lives at api.<root-domain>; util.loginUrl uses window for domain
    const res = await fetch((() => {
      const a = document.createElement('a'); a.href = root;
      const host = a.hostname;
      const m = host.match(/(^|\.)berjis\.(test|tech|com)$/i);
      const rootDomain = m ? `berjis.${m[2].toLowerCase()}` : 'berjis.test';
      return `${window.location.protocol}//api.${rootDomain}/v1/auth/verify`;
    })(), { credentials: 'include' });
    const j = await res.json();
    return !!(j?.data?.valid);
  } catch {
    return false;
  }
}

export const authGuard: CanActivateFn = async (_route, _state) => {
  const ok = await verifyAuth();
  if (ok) return true;
  // Redirect to centralized login with return URL
  window.location.href = loginUrl();
  return false;
};

