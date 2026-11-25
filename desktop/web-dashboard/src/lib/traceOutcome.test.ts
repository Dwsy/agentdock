import assert from 'node:assert/strict';
import test from 'node:test';
import type { TraceRecord } from '../api';
import { traceOutcome, type TraceOutcome } from './traceOutcome.ts';

const cases: [string, boolean, TraceRecord['command'], TraceOutcome][] = [
  ['ordinary success', true, undefined, 'success'],
  ['ordinary failure', false, undefined, 'error'],
  ['invocation failure overrides command success', false, { command_ok: true, exit_code: 0 }, 'error'],
  ['timeout flag overrides command success', true, { timed_out: true, command_ok: true }, 'error'],
  ['timeout status overrides zero exit', true, { status: 'timeout', exit_code: 0 }, 'error'],
  ['killed status overrides command success', true, { status: 'killed', command_ok: true }, 'error'],
  ['running with no terminal fields', true, { status: 'running' }, 'running'],
  ['command failure overrides stale running and zero exit', true, { status: 'running', command_ok: false, exit_code: 0 }, 'error'],
  ['command success overrides stale running and nonzero exit', true, { status: 'running', command_ok: true, exit_code: 1 }, 'success'],
  ['zero exit overrides stale running', true, { status: 'running', exit_code: 0 }, 'success'],
  ['nonzero exit overrides stale running', true, { status: 'running', exit_code: 2 }, 'error'],
  ['command with no outcome fields uses invocation success', true, { summary: 'command preview' }, 'success'],
];

for (const [name, success, command, expected] of cases) {
  test(name, () => assert.equal(traceOutcome({ success, command }), expected));
}
