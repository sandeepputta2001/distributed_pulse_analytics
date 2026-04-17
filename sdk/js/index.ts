/**
 * PulseAnalytics Browser & Node.js SDK
 *
 * @example
 * ```ts
 * import { PulseClient } from '@pulse-analytics/sdk';
 *
 * const pulse = new PulseClient({
 *   baseUrl: 'https://gateway.pulse-analytics.io',
 *   apiKey:  'your-api-key',
 *   appId:   'app_abc123',
 *   deviceId: navigator.userAgent, // or a stable UUID you generate
 * });
 *
 * pulse.track('purchase_completed', { price: 29.99, item_id: 'sku_999' });
 * await pulse.flush();
 * ```
 */

export interface PulseEvent {
  event_id?: string;
  event_name: string;
  event_time?: number; // epoch ms; defaults to Date.now()
  props?: Record<string, unknown>;
  revenue?: number;
}

export interface PulseConfig {
  /** Gateway base URL, e.g. "https://gateway.pulse-analytics.io" */
  baseUrl: string;
  /** API key issued for the app */
  apiKey: string;
  /** Application ID */
  appId: string;
  /** Stable device/session identifier */
  deviceId: string;
  /** Optional user ID (set after login) */
  userId?: string;
  /**
   * Max events per POST request (default: 100, max: 500).
   * flush() will automatically chunk larger queues into multiple requests.
   */
  maxBatch?: number;
  /** Auto-flush interval in ms (default: 2000) */
  flushIntervalMs?: number;
  /** SDK version reported in payloads */
  sdkVersion?: string;
  /** Max retry attempts on transient errors (default: 5) */
  maxRetries?: number;
}

export interface IdentifyPayload {
  user_id: string;
  traits?: Record<string, unknown>;
}

export interface IngestResponse {
  accepted: number;
  filtered: number;
}

// ── Retry / backoff constants ─────────────────────────────────────────────────
const BASE_BACKOFF_MS = 100;
const MAX_BACKOFF_MS = 30_000;
const MAX_CHUNK_SIZE = 500;

/** Full-jitter exponential backoff: sleep = random(0, min(cap, base * 2^attempt)) */
function jitterMs(attempt: number): number {
  const cap = Math.min(MAX_BACKOFF_MS, BASE_BACKOFF_MS * 2 ** attempt);
  return Math.random() * cap;
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export class PulseClient {
  private readonly cfg: Required<PulseConfig>;
  private queue: PulseEvent[] = [];
  private timer: ReturnType<typeof setInterval> | null = null;

  constructor(config: PulseConfig) {
    this.cfg = {
      maxBatch: 100,
      flushIntervalMs: 2000,
      sdkVersion: '1.0.0',
      userId: '',
      maxRetries: 5,
      ...config,
    };
    if (this.cfg.maxBatch > MAX_CHUNK_SIZE) this.cfg.maxBatch = MAX_CHUNK_SIZE;
    if (this.cfg.maxBatch < 1) this.cfg.maxBatch = 1;
    this.startTimer();
  }

  /**
   * Enqueues an event. Flushes automatically when the batch is full or the
   * flush interval elapses. Non-blocking.
   */
  track(eventName: string, props?: Record<string, unknown>, revenue?: number): void {
    const e: PulseEvent = {
      event_name: eventName,
      event_time: Date.now(),
      ...(props && { props }),
      ...(revenue !== undefined && { revenue }),
    };
    this.queue.push(e);
    if (this.queue.length >= this.cfg.maxBatch) {
      void this.flush();
    }
  }

  /**
   * Identifies a user and updates their profile traits.
   */
  async identify(userId: string, traits?: Record<string, unknown>): Promise<void> {
    this.cfg.userId = userId;
    const payload: IdentifyPayload = { user_id: userId, traits };
    await this.post('/v1/identify', payload);
  }

  /**
   * Flushes ALL buffered events to the gateway.
   *
   * Chunking: the entire queue is drained by sending successive POST requests
   * of at most maxBatch events each. This guarantees the server's 500-event
   * per-request limit is respected regardless of queue depth.
   *
   * Each chunk is delivered with retry + full-jitter exponential backoff.
   * 4xx errors (except 429) are not retried.
   */
  async flush(): Promise<void> {
    if (this.queue.length === 0) return;

    // Snapshot and drain the queue atomically so concurrent flush() calls
    // don't double-send the same events.
    const pending = this.queue.splice(0);

    // ── Chunking loop ─────────────────────────────────────────────────────
    // Send chunks of maxBatch until the snapshot is fully delivered.
    for (let offset = 0; offset < pending.length; offset += this.cfg.maxBatch) {
      const chunk = pending.slice(offset, offset + this.cfg.maxBatch);
      await this.sendChunkWithRetry(chunk);
    }
  }

  /**
   * Flushes remaining events and stops the background timer.
   * Call before page unload or process exit.
   */
  async shutdown(): Promise<void> {
    if (this.timer !== null) {
      clearInterval(this.timer);
      this.timer = null;
    }
    await this.flush();
  }

  private startTimer(): void {
    this.timer = setInterval(() => {
      void this.flush();
    }, this.cfg.flushIntervalMs);
    // Don't block Node.js process exit
    if (typeof this.timer === 'object' && 'unref' in this.timer) {
      (this.timer as NodeJS.Timeout).unref();
    }
  }

  /**
   * Delivers a single chunk with retry + full-jitter exponential backoff.
   *
   * Retry policy:
   *   - Network errors  → retry
   *   - HTTP 429        → retry (rate-limited; backoff gives gateway relief)
   *   - HTTP 5xx        → retry
   *   - HTTP 4xx (!429) → do NOT retry (permanent client error)
   */
  private async sendChunkWithRetry(chunk: PulseEvent[]): Promise<void> {
    let lastErr: unknown;
    for (let attempt = 0; attempt <= this.cfg.maxRetries; attempt++) {
      if (attempt > 0) {
        await sleep(jitterMs(attempt - 1));
      }
      try {
        await this.sendChunk(chunk);
        return; // success
      } catch (err) {
        lastErr = err;
        // Don't retry permanent client errors
        if (err instanceof HttpError && err.status >= 400 && err.status < 500 && err.status !== 429) {
          throw err;
        }
      }
    }
    // All retries exhausted — surface to caller so events can be counted as dropped
    throw new Error(
      `PulseAnalytics: all ${this.cfg.maxRetries} retries exhausted. Last error: ${String(lastErr)}`,
    );
  }

  private async sendChunk(events: PulseEvent[]): Promise<IngestResponse> {
    const body = {
      app_id: this.cfg.appId,
      device_id: this.cfg.deviceId,
      user_id: this.cfg.userId || undefined,
      sdk_version: this.cfg.sdkVersion,
      sent_at_ms: Date.now(),
      events,
    };
    return this.post<IngestResponse>('/v1/events', body);
  }

  private async post<T>(path: string, payload: unknown): Promise<T> {
    const response = await fetch(`${this.cfg.baseUrl}${path}`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-API-Key': this.cfg.apiKey,
      },
      body: JSON.stringify(payload),
      // Use keepalive for beacon-like semantics on page unload
      keepalive: true,
    });
    if (!response.ok) {
      throw new HttpError(response.status, path);
    }
    return response.json() as Promise<T>;
  }
}

/** Typed HTTP error so retry logic can distinguish status codes. */
class HttpError extends Error {
  constructor(
    public readonly status: number,
    path: string,
  ) {
    super(`PulseAnalytics: HTTP ${status} from ${path}`);
    this.name = 'HttpError';
  }
}

/** Convenience factory */
export function createClient(config: PulseConfig): PulseClient {
  return new PulseClient(config);
}

export default PulseClient;
