import test from 'node:test';
import assert from 'node:assert/strict';
import type { ManualLLMQueueEntry } from '../api.ts';
import { filterManualEntries, isManualEntryActive, resolveFullEndpoint, statusMeta } from './manualLLMQueue.ts';

function entry(status: string, id: string): ManualLLMQueueEntry {
  return {
    request_id: id,
    response_id: `resp_${id}`,
    session_id: `s_${id}`,
    model: 'manual',
    status,
    turn: 1,
    created_at: '2026-11-18T00:00:00Z',
    message_count: 1,
    new_message_count: 1,
    tool_count: 0,
    copy_prompt: `Call MCP tool llm_wait(request_id="${id}")`,
  };
}

test('识别活跃状态并过滤队列', () => {
  assert.equal(isManualEntryActive('queued'), true);
  assert.equal(isManualEntryActive('streaming'), true);
  assert.equal(isManualEntryActive('completed'), false);

  const items = [entry('queued', '1'), entry('streaming', '2'), entry('completed', '3'), entry('cancelled', '4')];
  assert.deepEqual(filterManualEntries(items, 'active').map((i) => i.request_id), ['1', '2']);
  assert.deepEqual(filterManualEntries(items, 'finished').map((i) => i.request_id), ['3', '4']);
  assert.equal(filterManualEntries(items, 'all').length, 4);
});

test('状态徽章与端点拼接格式正确', () => {
  assert.equal(statusMeta('queued').label, '待领取');
  assert.equal(statusMeta('completed').label, '已完成');
  assert.equal(resolveFullEndpoint('http://127.0.0.1:8765/', '/mcp/manual'), 'http://127.0.0.1:8765/mcp/manual');
  assert.equal(resolveFullEndpoint('http://127.0.0.1:8765', 'v1/responses'), 'http://127.0.0.1:8765/v1/responses');
});
