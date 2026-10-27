// Small coalescing queue for main→renderer IPC events.
//
// A streaming Agent emits many ACP session updates per second. Sending each one
// as its own webContents.send crosses the process boundary (structured clone +
// renderer task) per chunk. Batching a short window into one array keeps order
// and every event, but pays the IPC cost once per frame-sized slice.

export interface RendererEventBatchOptions {
  flushMs?: number;
  maxBatch?: number;
}

export class RendererEventBatcher<T> {
  private queue: T[] = [];
  private timer: ReturnType<typeof setTimeout> | undefined;
  private readonly send: (events: T[]) => void;
  private readonly flushMs: number;
  private readonly maxBatch: number;

  constructor(send: (events: T[]) => void, options: RendererEventBatchOptions = {}) {
    this.send = send;
    this.flushMs = options.flushMs && options.flushMs > 0 ? options.flushMs : 8;
    this.maxBatch = options.maxBatch && options.maxBatch > 0 ? options.maxBatch : 64;
  }

  get pending(): number {
    return this.queue.length;
  }

  enqueue(event: T): void {
    this.queue.push(event);
    if (this.queue.length >= this.maxBatch) {
      this.flush();
      return;
    }
    if (this.timer !== undefined) return;
    const timer = setTimeout(() => {
      this.timer = undefined;
      this.flush();
    }, this.flushMs) as ReturnType<typeof setTimeout> & { unref?: () => void };
    // A pending batch must never keep the Electron main process alive.
    timer.unref?.();
    this.timer = timer;
  }

  flush(): void {
    if (this.timer !== undefined) {
      clearTimeout(this.timer);
      this.timer = undefined;
    }
    if (this.queue.length === 0) return;
    const events = this.queue;
    this.queue = [];
    this.send(events);
  }

  dispose(): void {
    if (this.timer !== undefined) {
      clearTimeout(this.timer);
      this.timer = undefined;
    }
    this.queue = [];
  }
}
