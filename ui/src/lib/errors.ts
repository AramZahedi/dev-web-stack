export class ApiError extends Error {
  readonly code: string;
  readonly details?: unknown;
  readonly status: number;

  constructor(code: string, message: string, status = 0, details?: unknown) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.status = status;
    this.details = details;
  }
}

export function describeError(error: unknown): { code: string; message: string } {
  if (error instanceof ApiError) return { code: error.code, message: error.message };
  if (error instanceof Error) return { code: 'UNEXPECTED', message: error.message };
  return { code: 'UNEXPECTED', message: 'Something went wrong.' };
}
