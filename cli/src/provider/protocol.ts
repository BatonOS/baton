// SPDX-License-Identifier: Apache-2.0

















import { z } from 'zod';

export const OperationID = z.string().regex(/^op_[0-9a-f]{32}$/);

export const StepName = z.enum([
  'capture',
  'place',
  'enroll',
  'materialize',
  'file.write',
  'test.run',






  'provision',
  'runtime.read',
]);
export type StepName = z.infer<typeof StepName>;


export const StepParams = {
  capture: z.object({ source: z.string().min(1) }).strict(),
  place: z.object({ source: z.string().min(1), name: z.string().min(1) }).strict(),
  enroll: z.object({ name: z.string().min(1) }).strict(),
  materialize: z.object({ name: z.string().min(1), archive_operation_id: OperationID }).strict(),




  provision: z
    .object({ name: z.string().min(1), harness: z.string().min(1), placement: z.string().min(1) })
    .strict(),

  'runtime.read': z.object({ workspace: z.string().min(1) }).strict(),




  'file.write': z.object({
    workspace: z.string().min(1),
    path: z.string().min(1),
    content: z.string(),
    expected_sha_before: z.string().regex(/^sha256:[0-9a-f]{64}$/),
  }).strict(),
  'test.run': z.object({ workspace: z.string().min(1), path: z.string().min(1) }).strict(),
} as const;

export const ExecuteRequest = z.object({
  step: StepName,










  subject: z.string(),

  nonce: z.string().min(1),
  params: z.record(z.unknown()),
}).strict();








export interface Answer {
  operation_id: string;
  step: StepName;
  status: 'succeeded' | 'failed' | 'unknown';
  nonce: string;
  evidence: Record<string, unknown>;
  answered_from: 'execution' | 'log';

  provider_instance_id: string;
}













export interface NotFound {
  status: 'not_found';
  operation_id: string;
  provider_instance_id: string;
}















export interface ReceivedNoResult {
  status: 'received_no_result';
  operation_id: string;
  provider_instance_id: string;
}


export interface ProtocolError {
  code: string;
  message: string;
}











export interface GovernanceFact {
  name: string;
  value: string;
  source: string;

  resolved_at: string;
}






export const FACT_WORKSPACE_BOUNDARY = 'workspace_boundary';
export const FACT_DESTINATION_RESOLVED = 'destination_resolved';






export const FACT_HARNESSES_AVAILABLE = 'harnesses_available';
