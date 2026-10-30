import * as api from '../../api';
import type { DatabaseSiteRef } from '../../databaseTypes';

export type LinkedSiteState = 'unknown' | 'running' | 'stopped';

/** Reads the live state of the sites linked to a database. */
export async function readLinkedSiteStates(
  sites: DatabaseSiteRef[] | undefined,
): Promise<Record<string, LinkedSiteState>> {
  if (!sites || sites.length === 0) return {};
  const entries = await Promise.all(
    sites.map(async (site) => {
      try {
        const view = await api.getSite(site.id);
        const stopped = view.status === 'stopped' || view.desired_state === 'stopped';
        return [site.id, stopped ? 'stopped' : 'running'] as const;
      } catch {
        return [site.id, 'unknown'] as const;
      }
    }),
  );
  return Object.fromEntries(entries);
}

export function allLinkedSitesStopped(
  sites: DatabaseSiteRef[] | undefined,
  states: Record<string, LinkedSiteState>,
): boolean {
  if (!sites || sites.length === 0) return true;
  return sites.every((site) => states[site.id] === 'stopped');
}
