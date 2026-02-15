use std::sync::Arc;
use stripelistener::{Config, EventHandler, StripeListener, WebhookEvent, StripeEventPayload, V2Event, V2EventPayload, Logger};
use log::{info, warn, error, debug};
use env_logger::Env;

struct SimpleHandler;

impl EventHandler for SimpleHandler {
    fn on_webhook_event(&self, _evt: WebhookEvent, parsed: StripeEventPayload) {
        println!("Received webhook event: {} (ID: {})", parsed.event_type, parsed.id);
    }

    fn on_v2_event(&self, _evt: V2Event, parsed: V2EventPayload) {
        println!("Received v2 event: {} (ID: {})", parsed.event_type, parsed.id);
    }

    fn on_unknown_message(&self, raw_type: String, _data: serde_json::Value) {
        println!("Received unknown message type: {}", raw_type);
    }
}

struct ConsoleLogger;

impl Logger for ConsoleLogger {
    fn debug(&self, msg: &str) { debug!("{}", msg); }
    fn info(&self, msg: &str) { info!("{}", msg); }
    fn warn(&self, msg: &str) { warn!("{}", msg); }
    fn error(&self, msg: &str) { error!("{}", msg); }
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    env_logger::Builder::from_env(Env::default().default_filter_or("info")).init();

    let api_key = std::env::var("STRIPE_API_KEY").unwrap_or_default();
    if api_key.is_empty() {
        eprintln!("Please set STRIPE_API_KEY environment variable");
        return Ok(());
    }

    let config = Config {
        api_key,
        device_name: Some("rust-example-listener".to_string()),
        websocket_features: Some(vec!["webhooks".to_string()]),
        handler: Arc::new(SimpleHandler),
        logger: Some(Arc::new(ConsoleLogger)),
        pong_wait: None,
        ping_period: None,
    };

    let mut listener = StripeListener::new(config);

    println!("Authorizing...");
    listener.authorize().await?;
    
    println!("Connecting...");
    listener.connect().await?;

    println!("Listening for events (Ctrl+C to stop)...");
    // In a real app, you'd probably run this in a loop or handle reconnection
    // The current implementation's listen loop isn't fully exposed as a single blocking call yet
    // because `connect` spawns tasks. We need to keep the main thread alive.
    
    // Changing the library slightly to expose a `listen` method similar to Go would be good,
    // but for now let's just wait.
    
    tokio::signal::ctrl_c().await?;
    println!("Shutting down...");

    Ok(())
}
