package stripelistener

import "encoding/json"

// --- Session (from POST /v1/stripecli/sessions) ---

// Session is returned by Stripe when authorizing a CLI session.
// Source: https://github.com/stripe/stripe-cli/blob/master/pkg/stripeauth/messages.go
type Session struct {
	ReconnectDelay             int    `json:"reconnect_delay"`
	Secret                     string `json:"secret"`
	WebSocketAuthorizedFeature string `json:"websocket_authorized_feature"`
	WebSocketID                string `json:"websocket_id"`
	WebSocketURL               string `json:"websocket_url"`
	DefaultVersion             string `json:"default_version"`
	LatestVersion              string `json:"latest_version"`
}

// --- Incoming WebSocket messages ---

// WebhookEndpoint describes the fake endpoint attached to the event.
type WebhookEndpoint struct {
	APIVersion *string `json:"api_version"`
}

// WebhookEvent is a v1 webhook event pushed over the WebSocket.
// Source: https://github.com/stripe/stripe-cli/blob/master/pkg/websocket/webhook_messages.go
type WebhookEvent struct {
	Endpoint              WebhookEndpoint   `json:"endpoint"`
	EventPayload          string            `json:"event_payload"`
	HTTPHeaders           map[string]string `json:"http_headers"`
	Type                  string            `json:"type"`
	WebhookConversationID string            `json:"webhook_conversation_id"`
	WebhookID             string            `json:"webhook_id"`
}

// V2Event is a v2 thin event pushed over the WebSocket.
// Source: https://github.com/stripe/stripe-cli/blob/master/pkg/websocket/webhook_messages.go
type V2Event struct {
	Type               string            `json:"type"`
	HTTPHeaders        map[string]string `json:"http_headers"`
	Payload            string            `json:"payload"`
	EventDestinationID string            `json:"destination_id"`
}

// IncomingMessage is a polymorphic envelope for all WebSocket messages.
// Source: https://github.com/stripe/stripe-cli/blob/master/pkg/websocket/messages.go
type IncomingMessage struct {
	WebhookEvent *WebhookEvent
	V2Event      *V2Event
	RawType      string
	RawData      json.RawMessage
}

func (m *IncomingMessage) UnmarshalJSON(data []byte) error {
	typeOnly := struct {
		Type string `json:"type"`
	}{}
	if err := json.Unmarshal(data, &typeOnly); err != nil {
		return err
	}
	m.RawType = typeOnly.Type
	m.RawData = data

	switch typeOnly.Type {
	case "webhook_event":
		m.WebhookEvent = &WebhookEvent{}
		return json.Unmarshal(data, m.WebhookEvent)
	case "v2_event":
		m.V2Event = &V2Event{}
		return json.Unmarshal(data, m.V2Event)
	}
	return nil
}

// --- Outgoing WebSocket messages ---

// EventAck acknowledges receipt of an event.
// Source: https://github.com/stripe/stripe-cli/blob/master/pkg/websocket/messages.go#L71-L90
type EventAck struct {
	Type                  string `json:"type"`
	EventID               string `json:"event_id"`
	WebhookConversationID string `json:"webhook_conversation_id"`
	WebhookID             string `json:"webhook_id"`
}

// --- Parsed inner event payload ---

// StripeEventPayload is the parsed JSON inside WebhookEvent.EventPayload.
type StripeEventPayload struct {
	ID              string                 `json:"id"`
	Type            string                 `json:"type"`
	Created         int64                  `json:"created"`
	Livemode        bool                   `json:"livemode"`
	APIVersion      string                 `json:"api_version"`
	PendingWebhooks int                    `json:"pending_webhooks"`
	Data            map[string]interface{} `json:"data"`
}

// V2EventPayload is the parsed JSON inside V2Event.Payload.
type V2EventPayload struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}