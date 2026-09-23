// SPDX-License-Identifier: Apache-2.0










export const ExitCode = {
  OK: 0,

  CONFIG: 2,

  PRECONDITION: 3,

  UNREACHABLE: 4,

  AUTH: 5,

  CONFLICT: 6,

  PARTIAL: 7,

  UNSUPPORTED: 8,

  INTERNAL: 10,
} as const;

export type ExitCodeValue = (typeof ExitCode)[keyof typeof ExitCode];

export interface BatonErrorFields {
  code: string;
  message: string;





  remediation: string;
  exitCode: ExitCodeValue;
  requestId?: string;
  details?: Record<string, unknown>;

  cause?: unknown;
}


export class BatonError extends Error {
  readonly code: string;
  readonly remediation: string;
  readonly exitCode: ExitCodeValue;
  readonly requestId?: string;
  readonly details?: Record<string, unknown>;

  constructor(fields: BatonErrorFields) {
    super(fields.message, { cause: fields.cause });
    this.name = 'BatonError';
    this.code = fields.code;
    this.remediation = fields.remediation;
    this.exitCode = fields.exitCode;
    this.requestId = fields.requestId;
    this.details = fields.details;
  }

  toJSON(): Record<string, unknown> {
    return {
      code: this.code,
      message: this.message,
      request_id: this.requestId ?? '',
      details: this.details,
      remediation: this.remediation,
    };
  }
}


export const usageError = (message: string, remediation: string): BatonError =>
  new BatonError({ code: 'INVALID_ARGUMENT', message, remediation, exitCode: ExitCode.CONFIG });










export const unsupportedError = (message: string, remediation: string): BatonError =>
  new BatonError({
    code: 'NOT_IMPLEMENTED',
    message,
    remediation,
    exitCode: ExitCode.UNSUPPORTED,
  });


export const preconditionError = (
  message: string,
  remediation: string,
  cause?: unknown,
): BatonError =>
  new BatonError({
    code: 'PRECONDITION_FAILED',
    message,
    remediation,
    exitCode: ExitCode.PRECONDITION,
    cause,
  });








export function renderError(err: unknown, verbose = false): string {
  if (err instanceof BatonError) {
    const lines = [`error: ${err.message}`];
    if (err.remediation) lines.push(`  ${err.remediation}`);
    if (err.requestId) lines.push(`  request id: ${err.requestId}`);
    if (verbose && err.cause) lines.push(`  cause: ${String(err.cause)}`);
    return lines.join('\n');
  }
  if (err instanceof Error) {
    return verbose ? `error: ${err.stack ?? err.message}` : `error: ${err.message}`;
  }
  return `error: ${String(err)}`;
}

export function exitCodeFor(err: unknown): ExitCodeValue {
  return err instanceof BatonError ? err.exitCode : ExitCode.INTERNAL;
}
