package stripelistener

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	ws "github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// Timing defaults – match pkg/websocket/client.go constants
// https://github.com/stripe/stripe-cli/blob/master/pkg/websocket/client.go#L618-L625
// ---------------------------------------------------------------------------

const (
	DefaultPongWait      = 10 * time.Second
	DefaultPingPeriod    = (DefaultPongWait * 2) / 10 // 2s
	DefaultWriteWait     = 1 * time.Second
	DefaultReconnectWait = 10 * time.Second

	cliVersion  = "1.21.0"
	subprotocol = "stripecli-devproxy-v1"
	sessionPath = "/v1/stripecli/sessions"
	apiBase     = "https://api.stripe.com"
)

// ---------------------------------------------------------------------------
// EventHandler – the callback users implement
// ---------------------------------------------------------------------------

// EventHandler receives parsed events from the WebSocket stream.
type EventHandler interface {
	// OnWebhookEvent is called for every v1 webhook_event.
	OnWebhookEvent(evt WebhookEvent, parsed StripeEventPayload)

	// OnV2Event is called for every v2 thin event.
	OnV2Event(evt V2Event, parsed V2EventPayload)

	// OnUnknownMessage is called for message types the listener doesn't know.
	OnUnknownMessage(rawType string, data json.RawMessage)
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// Config configures the Listener.
type Config struct {
	// APIKey is the Stripe secret key (sk_test_... or sk_live_...). Required.
	APIKey string

	// DeviceName sent to Stripe during session creation. Optional.
	DeviceName string

	// WebSocketFeatures to request. Defaults to ["webhooks"].
	WebSocketFeatures []string

	// Handler receives events. Required.
	Handler EventHandler

	// Logger for debug output. Nil disables logging.
	Logger Logger

	// PongWait is how long to wait for a pong before considering the connection dead.
	PongWait time.Duration

	// PingPeriod is how often to send WebSocket pings.
	PingPeriod time.Duration

	// WriteWait is the deadline for writing a single frame.
	WriteWait time.Duration

	// HTTPClient used for the authorize request. Nil uses a default.
	HTTPClient *http.Client
}

func (c *Config) defaults() {
	if c.DeviceName == "" {
		c.DeviceName = "custom-stripe-listener"
	}
	if len(c.WebSocketFeatures) == 0 {
		c.WebSocketFeatures = []string{"webhooks"}
	}
	if c.PongWait == 0 {
		c.PongWait = DefaultPongWait
	}
	if c.PingPeriod == 0 {
		c.PingPeriod = DefaultPingPeriod
	}
	if c.WriteWait == 0 {
		c.WriteWait = DefaultWriteWait
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if c.Logger == nil {
		c.Logger = nopLogger{}
	}
}

// Logger is a minimal logging interface.
type Logger interface {
	Debugf(format string, args ...interface{})
	Infof(format string, args ...interface{})
	Warnf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}

type nopLogger struct{}

func (nopLogger) Debugf(string, ...interface{}) {}
func (nopLogger) Infof(string, ...interface{})  {}
func (nopLogger) Warnf(string, ...interface{})  {}
func (nopLogger) Errorf(string, ...interface{}) {}

// ---------------------------------------------------------------------------
// Listener
// ---------------------------------------------------------------------------

// Listener connects to Stripe's WebSocket endpoint and streams webhook events.
type Listener struct {
	cfg  Config
	conn *ws.Conn
	mu   sync.Mutex // guards conn writes

	session *Session
}

// New creates a Listener. Call Listen() to start.
func New(cfg Config) *Listener {
	cfg.defaults()
	return &Listener{cfg: cfg}
}

// Session returns the session obtained during Authorize. Nil before Authorize.
func (l *Listener) Session() *Session {
	return l.session
}

// ---------------------------------------------------------------------------
// Authorize – POST /v1/stripecli/sessions
// Source: https://github.com/stripe/stripe-cli/blob/master/pkg/stripeauth/client.go#L64-L129
// ---------------------------------------------------------------------------

// Authorize creates a CLI session with Stripe and returns the session data.
func (l *Listener) Authorize(ctx context.Context) (*Session, error) {
	form := url.Values{}
	form.Add("device_name", l.cfg.DeviceName)
	for _, f := range l.cfg.WebSocketFeatures {
		form.Add("websocket_features[]", f)
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		apiBase+sessionPath,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}

	setHeaders(req.Header, l.cfg.APIKey)

	resp, err := l.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("authorize request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read authorize response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("authorize failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var s Session
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("decode session: %w", err)
	}

	l.session = &s
	l.cfg.Logger.Infof("session created ws_id=%s feature=%s", s.WebSocketID, s.WebSocketAuthorizedFeature)
	return &s, nil
}

// ---------------------------------------------------------------------------
// Connect – WebSocket dial
// Source: https://github.com/stripe/stripe-cli/blob/master/pkg/websocket/client.go#L285-L334
// ---------------------------------------------------------------------------

// Connect dials the WebSocket. Call Authorize first.
func (l *Listener) Connect(ctx context.Context) error {
	if l.session == nil {
		return fmt.Errorf("call Authorize before Connect")
	}

	header := http.Header{}
	setHeaders(header, "")
	header.Set("Websocket-Id", l.session.WebSocketID)

	wsURL := l.session.WebSocketURL + "?websocket_feature=" + l.session.WebSocketAuthorizedFeature

	dialer := ws.Dialer{
		HandshakeTimeout: 10 * time.Second,
		Proxy:            http.ProxyFromEnvironment,
		Subprotocols:     []string{subprotocol},
	}

	l.cfg.Logger.Debugf("dialing %s", wsURL)
	conn, resp, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		extra := ""
		if resp != nil && resp.Body != nil {
			b, _ := io.ReadAll(resp.Body)
			extra = " | " + string(b)
		}
		return fmt.Errorf("websocket dial: %w%s", err, extra)
	}
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}

	l.conn = conn
	l.cfg.Logger.Infof("websocket connected")
	return nil
}

// ---------------------------------------------------------------------------
// Listen – blocking read loop + ping keep-alive
// ---------------------------------------------------------------------------

// Listen runs the event loop. Blocks until ctx is cancelled or an error occurs.
// Automatically sends ACKs and keep-alive pings.
func (l *Listener) Listen(ctx context.Context) error {
	if l.conn == nil {
		return fmt.Errorf("call Connect before Listen")
	}
	defer l.conn.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)

	// Ping loop
	go func() {
		if err := l.pingLoop(ctx); err != nil {
			errCh <- err
		}
	}()

	// Read loop
	go func() {
		errCh <- l.readLoop(ctx)
	}()

	select {
	case <-ctx.Done():
		l.close()
		return ctx.Err()
	case err := <-errCh:
		cancel()
		l.close()
		return err
	}
}

// ---------------------------------------------------------------------------
// ListenAll – convenience: Authorize + Connect + Listen in one call
// ---------------------------------------------------------------------------

// ListenAll is a convenience that calls Authorize, Connect, Listen sequentially.
func (l *Listener) ListenAll(ctx context.Context) error {
	if _, err := l.Authorize(ctx); err != nil {
		return err
	}
	if err := l.Connect(ctx); err != nil {
		return err
	}
	return l.Listen(ctx)
}

// ---------------------------------------------------------------------------
// Internals
// ---------------------------------------------------------------------------

func (l *Listener) readLoop(ctx context.Context) error {
	l.conn.SetPongHandler(func(string) error {
		return l.conn.SetReadDeadline(time.Now().Add(l.cfg.PongWait))
	})

	for {
		if err := l.conn.SetReadDeadline(time.Now().Add(l.cfg.PongWait)); err != nil {
			return fmt.Errorf("set read deadline: %w", err)
		}

		_, data, err := l.conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if ws.IsCloseError(err, ws.CloseNormalClosure) {
				return nil
			}
			return fmt.Errorf("read: %w", err)
		}

		var msg IncomingMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			l.cfg.Logger.Warnf("malformed message: %v", err)
			continue
		}

		switch {
		case msg.WebhookEvent != nil:
			var parsed StripeEventPayload
			_ = json.Unmarshal([]byte(msg.WebhookEvent.EventPayload), &parsed)
			l.sendACK(parsed.ID, msg.WebhookEvent.WebhookConversationID, msg.WebhookEvent.WebhookID)
			l.cfg.Handler.OnWebhookEvent(*msg.WebhookEvent, parsed)

		case msg.V2Event != nil:
			var parsed V2EventPayload
			_ = json.Unmarshal([]byte(msg.V2Event.Payload), &parsed)
			l.sendACK(parsed.ID, "", msg.V2Event.EventDestinationID)
			l.cfg.Handler.OnV2Event(*msg.V2Event, parsed)

		default:
			l.cfg.Handler.OnUnknownMessage(msg.RawType, msg.RawData)
		}
	}
}

func (l *Listener) pingLoop(ctx context.Context) error {
	ticker := time.NewTicker(l.cfg.PingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			l.mu.Lock()
			err := l.conn.WriteControl(ws.PingMessage, nil, time.Now().Add(l.cfg.WriteWait))
			l.mu.Unlock()
			if err != nil {
				return fmt.Errorf("ping: %w", err)
			}
		}
	}
}

func (l *Listener) sendACK(eventID, conversationID, webhookID string) {
	ack := EventAck{
		Type:                  "event_ack",
		EventID:               eventID,
		WebhookConversationID: conversationID,
		WebhookID:             webhookID,
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.conn.WriteJSON(ack); err != nil {
		l.cfg.Logger.Warnf("ack send failed for %s: %v", eventID, err)
	}
}

func (l *Listener) close() {
	if l.conn != nil {
		msg := ws.FormatCloseMessage(ws.CloseNormalClosure, "done")
		_ = l.conn.WriteControl(ws.CloseMessage, msg, time.Now().Add(l.cfg.WriteWait))
		time.Sleep(500 * time.Millisecond)
		l.conn.Close()
	}
}

// ---------------------------------------------------------------------------
// Shared headers – match the real CLI exactly
// Source: https://github.com/stripe/stripe-cli/blob/master/pkg/stripe/client.go#L88-L93
// Source: https://github.com/stripe/stripe-cli/blob/master/pkg/useragent/useragent.go#L56-L73
// ---------------------------------------------------------------------------

func setHeaders(h http.Header, apiKey string) {
	h.Set("Accept-Encoding", "identity")
	h.Set("User-Agent", "Stripe/v1 stripe-cli/"+cliVersion)

	ua, _ := json.Marshal(map[string]string{
		"name":      "stripe-cli",
		"version":   cliVersion,
		"publisher": "stripe",
		"os":        runtime.GOOS,
		"uname":     runtime.GOOS + " " + runtime.GOARCH,
	})
	h.Set("X-Stripe-Client-User-Agent", string(ua))

	if apiKey != "" {
		h.Set("Authorization", "Bearer "+apiKey)
		h.Set("Content-Type", "application/x-www-form-urlencoded")
	}
}
