export function detectRootDomain(hostname: string): string {
  const m = hostname.match(/(^|\.)berjis\.(test|tech|com)$/i);
  if (m) return `berjis.${m[2].toLowerCase()}`;
  // fallback
  return 'berjis.tech';
}

export function urlFor(sub: 'api'|'schools-api'|'schools', proto = window.location.protocol): string {
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

async function postJSON(url: string, body: any = {}): Promise<any> {
  const res = await fetch(url, {
    method: 'POST',
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json'
    },
    body: JSON.stringify(body ?? {})
  });
  if (!res.ok) return null;
  try {
    return await res.json();
  } catch {
    return null;
  }
}

async function refreshSession(): Promise<boolean> {
  try {
    const refreshRes = await postJSON(`${urlFor('api')}/v1/auth/refresh`);
    return !!refreshRes?.success;
  } catch {
    return false;
  }
}

export async function verifySession(opts: { attemptRefresh?: boolean } = {}): Promise<{ valid: boolean; data?: any }> {
  const attemptRefresh = !!opts.attemptRefresh;
  try {
    const res = await postJSON(`${urlFor('api')}/v1/auth/verify`);
    const valid = !!res?.data?.valid;
    if (valid || !attemptRefresh) {
      return { valid, data: res?.data };
    }
  } catch {
    if (!attemptRefresh) return { valid: false };
  }

  if (attemptRefresh) {
    const refreshed = await refreshSession();
    if (refreshed) {
      try {
        const res = await postJSON(`${urlFor('api')}/v1/auth/verify`);
        return { valid: !!res?.data?.valid, data: res?.data };
      } catch {}
    }
  }
  return { valid: false };
}
