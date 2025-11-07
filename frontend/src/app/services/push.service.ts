import { Injectable } from '@angular/core';
import { urlFor } from '../util';

@Injectable({ providedIn: 'root' })
export class PushService {
  private api = urlFor('schools-api');

  async ensureRegistration(): Promise<ServiceWorkerRegistration | null> {
    if (!('serviceWorker' in navigator)) return null;
    try { return await navigator.serviceWorker.register('/service-worker.js'); } catch { return null; }
  }

  async subscribe(vapidPublicKey?: string): Promise<boolean> {
    const reg = await this.ensureRegistration();
    if (!reg) return false;
    if (!('PushManager' in window)) return false;
    if (!vapidPublicKey) {
      try {
        const res = await fetch(`${this.api}/v1/push/public-key`, { credentials: 'include' });
        const j = await res.json();
        vapidPublicKey = j?.data?.vapidPublicKey || undefined;
      } catch { /* ignore */ }
      if (!vapidPublicKey) { try { vapidPublicKey = localStorage.getItem('webpush.vapid') || undefined; } catch {} }
    }
    const appServerKey = vapidPublicKey ? this.urlBase64ToUint8Array(vapidPublicKey) : undefined;
    try {
      const sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: appServerKey });
      const body = JSON.parse(JSON.stringify(sub));
      const res = await fetch(`${this.api}/v1/push/subscribe`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
      return !!(await res.json())?.success;
    } catch { return false; }
  }

  private urlBase64ToUint8Array(base64String: string): Uint8Array {
    const padding = '='.repeat((4 - (base64String.length % 4)) % 4);
    const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/');
    const rawData = atob(base64);
    const outputArray = new Uint8Array(rawData.length);
    for (let i = 0; i < rawData.length; ++i) outputArray[i] = rawData.charCodeAt(i);
    return outputArray;
  }
}
