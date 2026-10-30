const PREFIX = 'devstack.database.recovery.';

function key(serviceID: string): string {
  return `${PREFIX}${serviceID}`;
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
