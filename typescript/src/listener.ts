import WebSocket from "ws";
import type {
  Session,
  WebhookEvent,
  V2Event,
  IncomingMessage,
  EventAck,
  StripeEventPayload,
  V2EventPayload,
} from "./types.js";

// ──────────────────────────────────────────────────────────────
// Constants – match pkg/websocket/client.go defaults
// https://github.com/stripe/stripe-cli/blob/master/pkg/websocket/client.go#L618-L625
// ──────────────��───────────────────────────────────────────────

const CLI_VERSION = "1.21.0";
const SUBPROTOCOL = "stripecli-devproxy-v1";
const SESSION_PATH = "/v1/stripecli/sessions";
const API_BASE = "https://api.stripe.com";
const DEFAULT_PONG_WAIT_MS = 10_000;
const DEFAULT_PING_PERIOD_MS = 2_000;
const DEFAULT_WRITE_WAIT_MS = 1_000;

// ──────────────────────────────────────────────────────────────
// Logger interface
// ──────────────────────────────────────────────────────────────

export interface Logger {
  debug(msg: string, ...args: unknown[]): void;
  info(msg: string, ...args: unknown[]): void;
  warn(msg: string, ...args: unknown[]): void;
  error(msg: string, ...args: unknown[]): void;
}

const nopLogger: Logger = {
  debug() { },
  info() { },
  warn() { },
  error() { },
};

// ──────────────────────────────────────────────────────────────
// EventHandler – the callback users implement
// ──────────────────────────────────────────────────────────────

export interface EventHandler {
  onWebhookEvent(evt: WebhookEvent, parsed: StripeEventPayload): void;
  onV2Event(evt: V2Event, parsed: V2EventPayload): void;
  onUnknownMessage?(rawType: string, data: unknown): void;
}

// ──────────────────────────────────────────────────────────────
// Config
// ──────────────────────────────────────────────────────────────

export interface ListenerConfig {
  /** Stripe secret key (sk_test_... or sk_live_...). Required. */
  apiKey: string;

  /** Event handler. Required. */
  handler: EventHandler;

  /** Device name sent to Stripe. Optional. */
  deviceName?: string;

  /** WebSocket features to request. Default: ["webhooks"]. */
  webSocketFeatures?: string[];

  /** Logger for debug output. Optional. */
  logger?: Logger;

  /** Pong wait timeout in ms. Default: 10000. */
  pongWaitMs?: number;

  /** Ping interval in ms. Default: 2000. */
  pingPeriodMs?: number;
}

// ──────────────────────────────────────────────────────────────
// Shared headers – match the real CLI
// Source: https://github.com/stripe/stripe-cli/blob/master/pkg/stripe/client.go#L88-L93
// Source: https://github.com/stripe/stripe-cli/blob/master/pkg/useragent/useragent.go
// ──────────────────────────────────────────────────────────────

function makeHeaders(apiKey?: string): Record<string, string> {
  const h: Record<string, string> = {
    "Accept-Encoding": "identity",
    "User-Agent": `Stripe/v1 stripe-cli/${CLI_VERSION}`,
    "X-Stripe-Client-User-Agent": JSON.stringify({
      name: "stripe-cli",
      version: CLI_VERSION,
      publisher: "stripe",
      os: process.platform,
      uname: `${process.platform} ${process.arch}`,
    }),
  };
  if (apiKey) {
    h["Authorization"] = `Bearer ${apiKey}`;
    h["Content-Type"] = "application/x-www-form-urlencoded";
  }
  return h;
}

// ──────────────────────────────────────────────────────────────
// StripeListener class
// ──────────────────────────────────────────────────────────────

export class StripeListener {
  private cfg: Required<
    Pick<ListenerConfig, "apiKey" | "handler" | "deviceName" | "webSocketFeatures">
  > & {
    logger: Logger;
    pongWaitMs: number;
    pingPeriodMs: number;
  };

  private ws: WebSocket | null = null;
  private pingTimer: ReturnType<typeof setInterval> | null = null;
  private _session: Session | null = null;
  private abortController: AbortController | null = null;

  constructor(config: ListenerConfig) {
    this.cfg = {
      apiKey: config.apiKey,
      handler: config.handler,
      deviceName: config.deviceName ?? "custom-stripe-listener",
      webSocketFeatures: config.webSocketFeatures ?? ["webhooks"],
      logger: config.logger ?? nopLogger,
      pongWaitMs: config.pongWaitMs ?? DEFAULT_PONG_WAIT_MS,
      pingPeriodMs: config.pingPeriodMs ?? DEFAULT_PING_PERIOD_MS,
    };
  }

  /** The session obtained during authorize(). Null before authorize(). */
  get session(): Session | null {
    return this._session;
  }

  // ────────────────────────────────────────────────────────────
  // authorize – POST /v1/stripecli/sessions
  // Source: https://github.com/stripe/stripe-cli/blob/master/pkg/stripeauth/client.go#L64-L129
  // ────────────────────────────────────────────────────────────

  async authorize(signal?: AbortSignal): Promise<Session> {
    const params = new URLSearchParams();
    params.append("device_name", this.cfg.deviceName);
    for (const f of this.cfg.webSocketFeatures) {
      params.append("websocket_features[]", f);
    }

    const resp = await fetch(`${API_BASE}${SESSION_PATH}`, {
      method: "POST",
      headers: makeHeaders(this.cfg.apiKey),
      body: params.toString(),
      signal,
    });

    const body = await resp.text();
    if (!resp.ok) {
      throw new Error(`authorize failed (HTTP ${resp.status}): ${body}`);
    }

    this._session = JSON.parse(body) as Session;
    this.cfg.logger.info(
      `session created ws_id=${this._session.websocket_id} feature=${this._session.websocket_authorized_feature}`
    );
    return this._session;
  }

  // ────────────────────────────────────────────────────────────
  // connect – WebSocket dial
  // Source: https://github.com/stripe/stripe-cli/blob/master/pkg/websocket/client.go#L285-L334
  // ────────────────────────────────────────────────────────────

  connect(): Promise<void> {
    if (!this._session) {
      return Promise.reject(new Error("call authorize() before connect()"));
    }

    const session = this._session;
    const wsUrl =
      session.websocket_url +
      "?websocket_feature=" +
      session.websocket_authorized_feature;

    const headers = makeHeaders();
    headers["Websocket-Id"] = session.websocket_id;

    return new Promise<void>((resolve, reject) => {
      this.ws = new WebSocket(wsUrl, [SUBPROTOCOL], { headers });

      this.ws.once("open", () => {
        this.cfg.logger.info("websocket connected");
        this.startPing();
        resolve();
      });

      this.ws.once("error", (err) => {
        reject(new Error(`websocket connect error: ${err.message}`));
      });

      this.ws.on("message", (data: Buffer) => {
        this.handleMessage(data);
      });

      this.ws.on("pong", () => {
        this.cfg.logger.debug("pong received");
      });

      this.ws.on("close", (code, reason) => {
        this.cfg.logger.info(`websocket closed code=${code} reason=${reason}`);
        this.stopPing();
      });
    });
  }

  // ────────────────────────────────────────────────────────────
  // listen – Authorize + Connect in one call, returns a Promise
  //          that resolves when close() is called or connection drops
  // ────────────────────────────────────────────────────────────

  async listen(signal?: AbortSignal): Promise<void> {
    await this.authorize(signal);
    await this.connect();

    // Keep the listener alive until close() or abort
    return new Promise<void>((resolve) => {
      this.abortController = new AbortController();

      const onAbort = () => {
        this.close();
        resolve();
      };

      if (signal) {
        signal.addEventListener("abort", onAbort, { once: true });
      }

      this.ws?.once("close", () => {
        resolve();
      });
    });
  }

  // ────────────────────────────────────────────────────────────
  // close – graceful shutdown
  // ────────────────────────────────────────────────────────────

  close(): void {
    this.stopPing();
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.close(1000, "done");
    }
    this.ws = null;
  }

  // ────────────────────────────────────────────────────────────
  // Internals
  // ────────────────────────────────────────────────────────────

  private handleMessage(raw: Buffer): void {
    let msg: IncomingMessage;
    try {
      msg = JSON.parse(raw.toString()) as IncomingMessage;
    } catch {
      this.cfg.logger.warn(`malformed message: ${raw.toString()}`);
      return;
    }

    switch (msg.type) {
      case "webhook_event": {
        const evt = msg as WebhookEvent;
        let parsed: StripeEventPayload = { id: "", type: "", created: 0, livemode: false };
        try {
          parsed = JSON.parse(evt.event_payload);
        } catch {
          this.cfg.logger.warn("could not parse event_payload");
        }
        this.sendACK(parsed.id, evt.webhook_conversation_id, evt.webhook_id);
        this.cfg.handler.onWebhookEvent(evt, parsed);
        break;
      }

      case "v2_event": {
        const evt = msg as V2Event;
        let parsed: V2EventPayload = { id: "", type: "" };
        try {
          parsed = JSON.parse(evt.payload);
        } catch {
          this.cfg.logger.warn("could not parse v2 payload");
        }
        this.sendACK(parsed.id, "", evt.destination_id);
        this.cfg.handler.onV2Event(evt, parsed);
        break;
      }

      default:
        this.cfg.handler.onUnknownMessage?.(msg.type, msg);
        break;
    }
  }

  /**
   * Send event_ack back through the WebSocket.
   * Source: https://github.com/stripe/stripe-cli/blob/master/pkg/websocket/messages.go#L81-L90
   */
  private sendACK(
    eventId: string,
    conversationId: string,
    webhookId: string
  ): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;

    const ack: EventAck = {
      type: "event_ack",
      event_id: eventId,
      webhook_conversation_id: conversationId,
      webhook_id: webhookId,
    };

    this.ws.send(JSON.stringify(ack), (err) => {
      if (err) this.cfg.logger.warn(`ack failed for ${eventId}: ${err.message}`);
    });
  }

  /**
   * Ping keep-alive loop.
   * Source: https://github.com/stripe/stripe-cli/blob/master/pkg/websocket/client.go#L442-L551
   */
  private startPing(): void {
    this.pingTimer = setInterval(() => {
      if (this.ws && this.ws.readyState === WebSocket.OPEN) {
        this.ws.ping();
        this.cfg.logger.debug("ping sent");
      }
    }, this.cfg.pingPeriodMs);
  }

  private stopPing(): void {
    if (this.pingTimer) {
      clearInterval(this.pingTimer);
      this.pingTimer = null;
    }
  }
}