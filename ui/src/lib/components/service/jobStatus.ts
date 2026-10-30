import type { DatabaseJobStatus } from '../../databaseTypes';

export type JobToastKind = 'success' | 'error' | 'info';

/** Maps a terminal database job status to the toast severity shown to the user. */
export function jobToastKind(status: DatabaseJobStatus): JobToastKind {
  switch (status) {
    case 'succeeded':
      return 'success';
    case 'failed':
      return 'error';
    case 'canceled':
      return 'info';
    default:
      return 'info';
  }
}
