/**
 * Wire-format types matching the real Stripe CLI exactly.
 *
 * Session:       https://github.com/stripe/stripe-cli/blob/master/pkg/stripeauth/messages.go
 * WebhookEvent:  https://github.com/stripe/stripe-cli/blob/master/pkg/websocket/webhook_messages.go
 * V2Event:       https://github.com/stripe/stripe-cli/blob/master/pkg/websocket/webhook_messages.go
 * IncomingMsg:   https://github.com/stripe/stripe-cli/blob/master/pkg/websocket/messages.go
 * EventAck:      https://github.com/stripe/stripe-cli/blob/master/pkg/websocket/messages.go#L71-L90
 */

// --- POST /v1/stripecli/sessions response ---

export interface Session {
  reconnect_delay: number;
  secret: string;
  websocket_authorized_feature: string;
  websocket_id: string;
  websocket_url: string;
  default_version: string;
  latest_version: string;
}

// --- Incoming WebSocket messages ---

export interface WebhookEndpoint {
  api_version: string | null;
}

export interface WebhookEvent {
  endpoint: WebhookEndpoint;
  event_payload: string;
  http_headers: Record<string, string>;
  type: "webhook_event";
  webhook_conversation_id: string;
  webhook_id: string;
}

export interface V2Event {
  type: "v2_event";
  http_headers: Record<string, string>;
  payload: string;
  destination_id: string;
}

export type IncomingMessage =
  | WebhookEvent
  | V2Event
  | { type: string; [key: string]: unknown };

// --- Outgoing ---

export interface EventAck {
  type: "event_ack";
  event_id: string;
  webhook_conversation_id: string;
  webhook_id: string;
}

// --- Parsed inner payloads ---

export interface StripeEventPayload {
  id: string;
  type: string;
  created: number;
  livemode: boolean;
  api_version?: string;
  pending_webhooks?: number;
  data?: Record<string, unknown>;
  [key: string]: unknown;
}

export interface V2EventPayload {
  id: string;
  type: string;
  [key: string]: unknown;
}