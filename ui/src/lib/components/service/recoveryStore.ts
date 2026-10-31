const PREFIX = 'devstack.database.recovery.';
const ACTIVE_PREFIX = 'devstack.database.activeJob.';

function key(serviceID: string): string {
  return `${PREFIX}${serviceID}`;
}

function activeKey(serviceID: string): string {
  return `${ACTIVE_PREFIX}${serviceID}`;
}

export function loadRecoveryJobID(serviceID: string): string | null {
  try {
    return sessionStorage.getItem(key(serviceID));
  } catch {
    return null;
  }
}

export function saveRecoveryJobID(serviceID: string, jobID: string): void {
  try {
    sessionStorage.setItem(key(serviceID), jobID);
  } catch {
    // Session storage can be unavailable in private modes; the in-memory card
    // still works for the current page.
  }
}

export function clearRecoveryJobID(serviceID: string): void {
  try {
    sessionStorage.removeItem(key(serviceID));
  } catch {
    // Ignore unavailable storage.
  }
}

/** Persists the job the dashboard is polling so a reload can resume it. */
export function loadActiveJobID(serviceID: string): string | null {
  try {
    return sessionStorage.getItem(activeKey(serviceID));
  } catch {
    return null;
  }
}

export function saveActiveJobID(serviceID: string, jobID: string): void {
  try {
    sessionStorage.setItem(activeKey(serviceID), jobID);
  } catch {
    // Session storage can be unavailable in private modes; the in-memory job
    // card still works for the current page.
  }
}

export function clearActiveJobID(serviceID: string): void {
  try {
    sessionStorage.removeItem(activeKey(serviceID));
  } catch {
    // Ignore unavailable storage.
  }
}
