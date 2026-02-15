package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	sl "github.com/kmoz000/stripelistener/go"
)

// stdLogger adapts Go's log package to the Logger interface.
type stdLogger struct{}

func (stdLogger) Debugf(f string, a ...interface{}) { log.Printf("[DEBUG] "+f, a...) }
func (stdLogger) Infof(f string, a ...interface{})  { log.Printf("[INFO]  "+f, a...) }
func (stdLogger) Warnf(f string, a ...interface{})  { log.Printf("[WARN]  "+f, a...) }
func (stdLogger) Errorf(f string, a ...interface{}) { log.Printf("[ERROR] "+f, a...) }

// handler prints every event to stdout.
type handler struct{}

func (handler) OnWebhookEvent(evt sl.WebhookEvent, parsed sl.StripeEventPayload) {
	pretty, _ := json.MarshalIndent(json.RawMessage(evt.EventPayload), "  ", "  ")
	fmt.Printf("──── %s [%s] ────\n  %s\n\n", parsed.Type, parsed.ID, string(pretty))
}

func (handler) OnV2Event(evt sl.V2Event, parsed sl.V2EventPayload) {
	pretty, _ := json.MarshalIndent(json.RawMessage(evt.Payload), "  ", "  ")
	fmt.Printf("──── V2 %s [%s] ────\n  %s\n\n", parsed.Type, parsed.ID, string(pretty))
}

func (handler) OnUnknownMessage(rawType string, data json.RawMessage) {
	fmt.Printf("──── UNKNOWN type=%s ────\n  %s\n\n", rawType, string(data))
}

func main() {
	key := os.Getenv("STRIPE_API_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "export STRIPE_API_KEY=sk_test_...")
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; cancel() }()

	listener := sl.New(sl.Config{
		APIKey:  key,
		Handler: handler{},
		Logger:  stdLogger{},
	})

	// One-liner: authorize → connect → listen
	if err := listener.ListenAll(ctx); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}

	fmt.Println("Done.")
}
