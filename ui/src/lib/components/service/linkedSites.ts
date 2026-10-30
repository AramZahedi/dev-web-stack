import * as api from '../../api';
import type { DatabaseSiteRef } from '../../databaseTypes';

export type LinkedSiteState = 'unknown' | 'running' | 'stopping' | 'stopped';

/** Reads the live state of the sites linked to a database. */
export async function readLinkedSiteStates(
  sites: DatabaseSiteRef[] | undefined,
): Promise<Record<string, LinkedSiteState>> {
  if (!sites || sites.length === 0) return {};
  const entries = await Promise.all(
    sites.map(async (site) => {
      try {
        const view = await api.getSite(site.id);
        if (view.status === 'stopped') return [site.id, 'stopped'] as const;
        if (view.status === 'stopping') return [site.id, 'stopping'] as const;
        return [site.id, 'running'] as const;
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
