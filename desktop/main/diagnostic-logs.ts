// Bounded in-memory diagnostic log buffer for the Desktop main process.
// These logs are local-process diagnostics for the ACP child lifecycle and
// renderer errors. The in-memory buffer is not a persisted store or any
// ACP/Serve canonical record; the pre-existing file sink remains separate.

export interface DiagnosticLogEntry {
  id: number;
  timestamp: string;
  source: 'desktop' | 'acp' | 'renderer';
  message: string;
}

// Conservative secret masking. Covers common key/value and Authorization header
// shapes before lines leave the main process.
const SENSITIVE_KEYS = ['token', 'apikey', 'api_key', 'api-key', 'key', 'secret', 'password', 'authorization', 'bearer'];
const SENSITIVE_KEY_RE = new RegExp(
  `(\\b(?:${SENSITIVE_KEYS.join('|')})\\s*[:=]\\s*)[^\\s'"\`,;}\\]]+`,
  'gi',
);

export function maskSecrets(line: string): string {
  return line
    .replace(/(\bAuthorization\s*:\s*)([^,\n]*)/gi, '$1***')
    .replace(/\b(Bearer\s+)[^\s'"`,;\]})]+/gi, '$1***')
    .replace(/([?&](?:api[_-]?key|token|access_token|secret|password|authorization)=)([^&\s]*)/gi, '$1***')
    .replace(SENSITIVE_KEY_RE, '$1***');
}

export interface DiagnosticLogBufferOptions {
  maxEntries?: number;
}

export class DiagnosticLogBuffer {
  private entries: DiagnosticLogEntry[] = [];
  private nextId = 1;
  private readonly maxEntries: number;
  private subscribers = new Set<(entry: DiagnosticLogEntry) => void>();

  constructor(options: DiagnosticLogBufferOptions = {}) {
    this.maxEntries = options.maxEntries && options.maxEntries > 0 ? options.maxEntries : 400;
  }

  // Stores the message exactly as given. Callers are responsible for masking
  // sensitive values before appending; the canonical caller is the logger
  // created by `createDiagnosticLogger`, which applies `maskSecrets` once for
  // console, file, buffer, and renderer projections.
  append(source: DiagnosticLogEntry['source'], message: string): DiagnosticLogEntry {
    const entry: DiagnosticLogEntry = {
      id: this.nextId++,
      timestamp: new Date().toISOString(),
      source,
      message,
    };
    this.entries.push(entry);
    if (this.entries.length > this.maxEntries) {
      this.entries.shift();
    }
    for (const subscriber of this.subscribers) {
      try {
        subscriber(entry);
      } catch {
        // Subscriber errors must not break the logging path.
      }
    }
    return entry;
  }

  snapshot(): DiagnosticLogEntry[] {
    return this.entries.slice();
  }

  subscribe(callback: (entry: DiagnosticLogEntry) => void): () => void {
    this.subscribers.add(callback);
    return () => {
      this.subscribers.delete(callback);
    };
  }

  clear(): void {
    this.entries = [];
  }
}

export interface DiagnosticLogger {
  log(message: string, source?: DiagnosticLogEntry['source']): void;
}

// Keep a single long-lived desktop.log bounded across launches. The startup
// check rotates the previous run before appending a new one; the ACP child only
// writes lifecycle/error lines, so this avoids unbounded disk growth without
// adding a write-path syscall per line.
export const DESKTOP_LOG_ROTATE_BYTES = 8 * 1024 * 1024;

export function shouldRotateDesktopLog(sizeBytes: number): boolean {
  return Number.isFinite(sizeBytes) && sizeBytes > DESKTOP_LOG_ROTATE_BYTES;
}

// The file/console/buffer sinks always record diagnostics. The renderer only
// needs live entries while its log viewer is mounted, so this gate prevents a
// chatty ACP child from crossing the IPC boundary for every line in the
// background.
export class DiagnosticStreamGate {
  private enabled = false;

  setEnabled(enabled: boolean): void {
    this.enabled = enabled === true;
  }

  get enabledForRenderer(): boolean {
    return this.enabled;
  }
}

export interface DiagnosticLoggerDeps {
  buffer: DiagnosticLogBuffer;
  getLogStream: () => { write: (data: string) => void } | undefined;
  consoleLog?: (message: string) => void;
}

// Creates the canonical diagnostic logger for the Desktop main process. This
// is the single masking path: secrets are masked once here, and the same masked
// text is written to console, desktop.log, and the in-memory buffer (which is
// then projected to the renderer). No other code should mask diagnostic log
// content or write raw diagnostic lines to these sinks.
export function createDiagnosticLogger(deps: DiagnosticLoggerDeps): DiagnosticLogger {
  const { buffer, getLogStream, consoleLog = console.log } = deps;
  return {
    log(message: string, source: DiagnosticLogEntry['source'] = 'desktop'): void {
      const masked = maskSecrets(message);
      const timestamp = new Date().toISOString();
      const line = `${timestamp} [${source}] ${masked}\n`;
      consoleLog(line.trimEnd());
      buffer.append(source, masked);
      try {
        getLogStream()?.write(line);
      } catch {
        // Logging must never interfere with the ACP child lifecycle.
      }
    },
  };
}
