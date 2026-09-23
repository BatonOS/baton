// SPDX-License-Identifier: Apache-2.0



export type OutputFormat = 'table' | 'json';

export interface Column<T> {
  header: string;
  get: (row: T) => string;

  right?: boolean;
}










export function titled(title: string, body: string): string {


  return `\n\x1b[1m${title}\x1b[0m\n${'─'.repeat(title.length)}\n${body}`;
}

export function table<T>(rows: T[], columns: Column<T>[], emptyNote: string): string {
  if (rows.length === 0) return emptyNote;

  const cells = rows.map((r) => columns.map((c) => c.get(r) ?? ''));
  const widths = columns.map((c, i) =>
    Math.max(c.header.length, ...cells.map((row) => (row[i] ?? '').length)),
  );

  const pad = (text: string, width: number, right?: boolean) =>
    right ? text.padStart(width) : text.padEnd(width);

  const lines = [
    columns.map((c, i) => pad(c.header.toUpperCase(), widths[i]!, c.right)).join('  '),
    ...cells.map((row) =>
      row.map((cell, i) => pad(cell, widths[i]!, columns[i]!.right)).join('  '),
    ),
  ];
  return lines.join('\n').replace(/[ \t]+$/gm, '');
}

export function json(value: unknown): string {
  return JSON.stringify(value, null, 2);
}


export function age(seconds: number | null | undefined): string {


  if (seconds === null || seconds === undefined || seconds < 0) return '-';
  if (seconds < 60) return `${Math.floor(seconds)}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86_400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86_400)}d ago`;
}


export function statusText(status: string): string {
  if (!process.stdout.isTTY || process.env.NO_COLOR) return status;
  const colour: Record<string, string> = {
    active: '32',
    pending: '33',
    offline: '31',
    revoked: '35',
    succeeded: '32',
    failed: '31',
    expired: '33',
    queued: '36',
    dispatched: '36',
  };
  const code = colour[status];
  return code ? `[${code}m${status}[0m` : status;
}
