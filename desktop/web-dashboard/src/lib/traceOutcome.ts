import type { TraceRecord } from '../api';

export type TraceOutcome = 'success' | 'error' | 'running';

export function traceOutcome({ success, command }: Pick<TraceRecord, 'success' | 'command'>): TraceOutcome {
  if (!success || command?.timed_out || command?.status === 'timeout' || command?.status === 'killed') return 'error';
  // An async snapshot can still say running after a terminal result is available.
  if (command?.command_ok != null) return command.command_ok ? 'success' : 'error';
  if (command?.exit_code != null) return command.exit_code === 0 ? 'success' : 'error';
  return command?.status === 'running' ? 'running' : 'success';
}
