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

export interface DescribedError {
  code: string;
  message: string;
  details?: unknown;
}

export function describeError(error: unknown): DescribedError {
  if (error instanceof ApiError) {
    return { code: error.code, message: error.message, details: error.details };
  }
  if (error instanceof Error) return { code: 'UNEXPECTED', message: error.message };
  return { code: 'UNEXPECTED', message: 'Something went wrong.' };
}

export interface PHPConfigErrorDetails {
  line?: number;
  output?: string;
  field?: string;
  message?: string;
}

export interface PHPErrorDescription extends DescribedError {
  details?: PHPConfigErrorDetails;
}

/** Reads the structured details carried by PHP_CONFIG_INVALID responses. */
export function describePHPError(error: unknown): PHPErrorDescription {
  const described = describeError(error);
  if (!described.details || typeof described.details !== 'object') {
    return { ...described, details: undefined };
  }
  const raw = described.details as Record<string, unknown>;
  const details: PHPConfigErrorDetails = {};
  if (typeof raw.line === 'number' && Number.isFinite(raw.line)) details.line = raw.line;
  if (typeof raw.output === 'string') details.output = raw.output;
  if (typeof raw.field === 'string') details.field = raw.field;
  if (typeof raw.message === 'string') details.message = raw.message;
  return { ...described, details };
}
