// A federated sign-in in flight: what the console needs when the identity
// provider sends the browser back to /console/sign-in/callback/<connector>.
// Kept in this tab's sessionStorage, keyed by nothing but the one flow; the
// API's flow row is what makes the state single use, not this.

export interface Pending {
  state: string;
  connector: string;
  /** The tenant the operator named, where the deployment has several. */
  tenant?: string;
  /** Where to go once signed in: a console URL (paths.safeReturn). */
  returnTo: string;
}

const KEY = "hoplock.console.sign-in";

export function savePending(p: Pending): void {
  try {
    window.sessionStorage.setItem(KEY, JSON.stringify(p));
  } catch {
    // Without storage the callback says the sign-in expired, which is true
    // of this tab.
  }
}

/** The flow in flight, once: reading it clears it. */
export function takePending(): Pending | null {
  try {
    const raw = window.sessionStorage.getItem(KEY);
    window.sessionStorage.removeItem(KEY);
    return raw ? (JSON.parse(raw) as Pending) : null;
  } catch {
    return null;
  }
}
