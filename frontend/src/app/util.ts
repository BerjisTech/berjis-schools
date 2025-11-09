import { EnvironmentInjector } from '@angular/core';
import { CoreAuthService, CoreAuthSession } from '@berjis/angular-auth';
import { environment } from '../environments/environment';

let appInjector: EnvironmentInjector | null = null;
let sharedAuth: CoreAuthService | null = null;

function getAuth(): CoreAuthService {
  if (sharedAuth) {
    return sharedAuth;
  }
  if (!appInjector) {
    throw new Error('CoreAuthService is not initialized. Call initAuthService() from bootstrap.');
    }
  sharedAuth = appInjector.get(CoreAuthService);
  return sharedAuth;
}

export function initAuthService(injector: EnvironmentInjector) {
  appInjector = injector;
  sharedAuth = injector.get(CoreAuthService);
}

export function detectRootDomain(hostname: string): string {
  const m = hostname.match(/(^|\.)berjis\.(test|tech|com)$/i);
  if (m) return `berjis.${m[2].toLowerCase()}`;
  // fallback
  return 'berjis.tech';
}

export function urlFor(sub: 'api'|'schools-api'|'schools'|'ai'|'ai-api', proto = window.location.protocol): string {
  if (sub === 'api' && environment.apiBase) {
    return environment.apiBase;
  }
  if (sub === 'schools-api' && environment.schoolsApiBase) {
    return environment.schoolsApiBase;
  }
  const root = detectRootDomain(window.location.hostname);
  const host = sub === 'schools' ? `schools.${root}` : `${sub}.${root}`;
  return `${proto}//${host}`;
}

export function loginUrl(returnTo?: string): string {
  const root = detectRootDomain(window.location.hostname);
  const base = `${window.location.protocol}//${root}`;
  const ret = encodeURIComponent(returnTo ?? window.location.href);
  return `${base}/auth/login?returnUrl=${ret}`;
}

export async function verifySession(opts: { attemptRefresh?: boolean } = {}): Promise<{ valid: boolean; data?: CoreAuthSession }> {
  try {
    const auth = getAuth();
    const session = await auth.ensureAuth({ maxAgeMs: 1500, force: !!opts.attemptRefresh });
    return { valid: session.valid, data: session };
  } catch {
    return { valid: false };
  }
}

// App roles helper: checks if the user has a specific role for an app
export async function hasAppRole(app: string, role: string): Promise<boolean> {
  try {
    const auth = getAuth();
    const session = await auth.ensureAuth({ maxAgeMs: 1500 });
    if (session.valid && sessionHasAppRole(session, app, role)) {
      return true;
    }
  } catch {
    // ignore and fall back to remote probe
  }
  try {
    const res = await fetch(`${urlFor('api')}/v1/apps/${encodeURIComponent(app)}/roles`, { credentials: 'include' });
    if (!res.ok) return false;
    const json = await res.json();
    const list: string[] = json?.data ?? [];
    const key = `${app}.${role}`;
    return list.includes(key) || list.includes(role);
  } catch {
    return false;
  }
}

// Check if the user has any school membership for a given role ('tutor'|'admin')
export async function hasAnySchoolRole(role: 'tutor'|'admin'): Promise<boolean> {
  try {
    const res = await fetch(`${urlFor('schools-api')}/v1/schools/mine?role=${encodeURIComponent(role)}`, { credentials: 'include' });
    if (!res.ok) return false;
    const json = await res.json();
    const list: any[] = json?.data ?? [];
    return Array.isArray(list) && list.length > 0;
  } catch {
    return false;
  }
}

export interface AuthedUserProfile {
  uuid?: string;
  email?: string;
  name?: string;
  username?: string;
  preferences?: any;
  [key: string]: any;
}

let cachedUserProfilePromise: Promise<AuthedUserProfile | null> | null = null;

export async function fetchUserProfile(opts: { force?: boolean } = {}): Promise<AuthedUserProfile | null> {
  const force = !!opts.force;
  if (!force && cachedUserProfilePromise) {
    return cachedUserProfilePromise;
  }
  cachedUserProfilePromise = (async () => {
    try {
      const res = await fetch(`${urlFor('api')}/v1/me`, { credentials: 'include' });
      if (!res.ok) return null;
      const json = await res.json();
      const data = json?.data;
      return data && typeof data === 'object' ? data as AuthedUserProfile : null;
    } catch {
      return null;
    }
  })();
  return cachedUserProfilePromise;
}

function sessionHasAppRole(session: CoreAuthSession, app: string, role: string): boolean {
  const normalized = role;
  const prefixed = `${app}.${role}`;
  const underscore = `${app}_${role}`;
  const direct = session.appRoles?.[app] ?? [];
  if (direct.some(r => r === normalized || r === prefixed || r === underscore)) {
    return true;
  }
  const allRoles = new Set<string>([
    ...session.roles,
    ...session.platformRoles,
  ]);
  return allRoles.has(normalized) || allRoles.has(prefixed) || allRoles.has(underscore);
}
