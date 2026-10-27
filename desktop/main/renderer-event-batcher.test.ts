import assert from 'node:assert/strict';
import test from 'node:test';

import { RendererEventBatcher } from './renderer-event-batcher.ts';

test('renderer events are delivered in order as one batch', () => {
  const batches: number[][] = [];
  const batcher = new RendererEventBatcher<number>((events) => batches.push(events));
  try {
    batcher.enqueue(1);
    batcher.enqueue(2);
    batcher.enqueue(3);
    assert.equal(batches.length, 0, 'enqueue must not send immediately');
    batcher.flush();
    assert.deepEqual(batches, [[1, 2, 3]]);
    assert.equal(batcher.pending, 0);
  } finally {
    batcher.dispose();
  }
});

test('a full batch flushes without waiting for the timer', () => {
  const batches: number[][] = [];
  const batcher = new RendererEventBatcher<number>((events) => batches.push(events), { maxBatch: 2 });
  try {
    batcher.enqueue(1);
    batcher.enqueue(2);
    assert.deepEqual(batches, [[1, 2]]);
    batcher.enqueue(3);
    assert.equal(batches.length, 1);
    batcher.flush();
    assert.deepEqual(batches, [[1, 2], [3]]);
  } finally {
    batcher.dispose();
  }
});

test('flush is a no-op when nothing is queued and dispose drops pending events', () => {
  const batches: number[][] = [];
  const batcher = new RendererEventBatcher<number>((events) => batches.push(events));
  try {
    batcher.flush();
    assert.equal(batches.length, 0);
    batcher.enqueue(1);
    batcher.dispose();
    batcher.flush();
    assert.equal(batches.length, 0);
  } finally {
    batcher.dispose();
  }
});
