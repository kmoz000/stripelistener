use std::sync::{Arc, Mutex};
use std::time::Duration;

use futures_util::{SinkExt, StreamExt};
use reqwest::header::{HeaderMap, HeaderValue, AUTHORIZATION, CONTENT_TYPE, USER_AGENT};
use serde::{Deserialize, Serialize};

use tokio::time::interval;
use tokio_tungstenite::{connect_async, tungstenite::protocol::Message};
use url::Url;

// Constants matching pkg/websocket/client.go defaults
const CLI_VERSION: &str = "1.21.0";
const SUBPROTOCOL: &str = "stripecli-devproxy-v1";
const SESSION_PATH: &str = "/v1/stripecli/sessions";
const API_BASE: &str = "https://api.stripe.com";
const DEFAULT_PONG_WAIT: Duration = Duration::from_secs(10);
const DEFAULT_PING_PERIOD: Duration = Duration::from_secs(2);
// const DEFAULT_WRITE_WAIT: Duration = Duration::from_secs(1);

// Logger trait
pub trait Logger: Send + Sync {
    fn debug(&self, msg: &str);
    fn info(&self, msg: &str);
    fn warn(&self, msg: &str);
    fn error(&self, msg: &str);
}

pub struct NopLogger;
impl Logger for NopLogger {
    fn debug(&self, _msg: &str) {}
    fn info(&self, _msg: &str) {}
    fn warn(&self, _msg: &str) {}
    fn error(&self, _msg: &str) {}
}

// EventHandler trait
pub trait EventHandler: Send + Sync {
    fn on_webhook_event(&self, evt: WebhookEvent, parsed: StripeEventPayload);
    fn on_v2_event(&self, evt: V2Event, parsed: V2EventPayload);
    fn on_unknown_message(&self, raw_type: String, data: serde_json::Value);
}

// Configuration
pub struct Config {
    pub api_key: String,
    pub device_name: Option<String>,
    pub websocket_features: Option<Vec<String>>,
    pub handler: Arc<dyn EventHandler>,
    pub logger: Option<Arc<dyn Logger>>,
    pub pong_wait: Option<Duration>,
    pub ping_period: Option<Duration>,
}

impl Config {
    fn defaults(&mut self) {
        if self.device_name.is_none() {
            self.device_name = Some("custom-stripe-listener".to_string());
        }
        if self.websocket_features.is_none() {
            self.websocket_features = Some(vec!["webhooks".to_string()]);
        }
        if self.pong_wait.is_none() {
            self.pong_wait = Some(DEFAULT_PONG_WAIT);
        }
        if self.ping_period.is_none() {
            self.ping_period = Some(DEFAULT_PING_PERIOD);
        }
        if self.logger.is_none() {
            self.logger = Some(Arc::new(NopLogger));
        }
    }
}

// Data structures
#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct Session {
    pub websocket_id: String,
    pub websocket_url: String,
    pub websocket_authorized_feature: String,
}

#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct IncomingMessage {
    #[serde(rename = "type")]
    pub msg_type: String,
    #[serde(flatten)]
    pub data: serde_json::Value,
}

#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct WebhookEvent {
    pub webhook_id: String,
    pub webhook_conversation_id: String,
    pub event_payload: String,
    #[serde(flatten)]
    pub extra: serde_json::Value,
}

#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct V2Event {
    pub destination_id: String,
    pub payload: String,
    #[serde(flatten)]
    pub extra: serde_json::Value,
}

#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct StripeEventPayload {
    pub id: String,
    #[serde(rename = "type")]
    pub event_type: String,
    pub created: u64,
    pub livemode: bool,
}

#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct V2EventPayload {
    pub id: String,
    #[serde(rename = "type")]
    pub event_type: String,
}

#[derive(Serialize, Debug)]
struct EventAck {
    #[serde(rename = "type")]
    msg_type: String,
    event_id: String,
    webhook_conversation_id: String,
    webhook_id: String,
}

// Listener
pub struct StripeListener {
    cfg: Config,
    session: Option<Session>,
    write_tx: Option<tokio::sync::mpsc::Sender<Message>>,
}

impl StripeListener {
    pub fn new(mut cfg: Config) -> Self {
        cfg.defaults();
        Self {
            cfg,
            session: None,
            write_tx: None,
        }
    }

    pub fn session(&self) -> Option<&Session> {
        self.session.as_ref()
    }

    pub async fn authorize(&mut self) -> Result<Session, Box<dyn std::error::Error>> {
        let client = reqwest::Client::new();
        let mut params = Vec::new();

        if let Some(name) = &self.cfg.device_name {
            params.push(("device_name", name.as_str()));
        }
        if let Some(features) = &self.cfg.websocket_features {
            for f in features {
                params.push(("websocket_features[]", f.as_str()));
            }
        }

        let mut headers = HeaderMap::new();
        headers.insert("Accept-Encoding", HeaderValue::from_static("identity"));
        headers.insert("User-Agent", HeaderValue::from_str(&format!("Stripe/v1 stripe-cli/{}", CLI_VERSION))?);
        headers.insert("X-Stripe-Client-User-Agent", HeaderValue::from_str(&serde_json::json!({
            "name": "stripe-cli",
            "version": CLI_VERSION,
            "publisher": "stripe",
            "os": std::env::consts::OS,
            "uname": format!("{} {}", std::env::consts::OS, std::env::consts::ARCH),
        }).to_string())?);
        
        if !self.cfg.api_key.is_empty() {
            headers.insert(AUTHORIZATION, HeaderValue::from_str(&format!("Bearer {}", self.cfg.api_key))?);
            headers.insert(CONTENT_TYPE, HeaderValue::from_static("application/x-www-form-urlencoded"));
        }

        let resp = client.post(format!("{}{}", API_BASE, SESSION_PATH))
            .headers(headers)
            .form(&params)
            .send()
            .await?;

        if !resp.status().is_success() {
            let status = resp.status();
            let text = resp.text().await?;
            return Err(format!("authorize failed (HTTP {}): {}", status, text).into());
        }

        let session: Session = resp.json().await?;
        self.cfg.logger.as_ref().unwrap().info(&format!("session created ws_id={} feature={}", session.websocket_id, session.websocket_authorized_feature));
        self.session = Some(session.clone());
        Ok(session)
    }

    pub async fn connect(&mut self) -> Result<(), Box<dyn std::error::Error>> {
        let session = self.session.as_ref().ok_or("call authorize() before connect()")?;
        let ws_url = format!("{}?websocket_feature={}", session.websocket_url, session.websocket_authorized_feature);
        
        let url = Url::parse(&ws_url)?;
        let _host = url.host_str().ok_or("invalid websocket url")?;

        let mut headers = HeaderMap::new();
        headers.insert("Websocket-Id", HeaderValue::from_str(&session.websocket_id)?);
         // Standard headers
        headers.insert("User-Agent", HeaderValue::from_str(&format!("Stripe/v1 stripe-cli/{}", CLI_VERSION))?);
        headers.insert("X-Stripe-Client-User-Agent", HeaderValue::from_str(&serde_json::json!({
            "name": "stripe-cli",
            "version": CLI_VERSION,
            "publisher": "stripe",
            "os": std::env::consts::OS,
            "uname": format!("{} {}", std::env::consts::OS, std::env::consts::ARCH),
        }).to_string())?);
        // Add Authorization header if API key is present (though connect usually doesn't need it if session is valid?)
        // The Go code clears headers then sets Websocket-Id. 

        let request = tokio_tungstenite::tungstenite::handshake::client::Request::builder()
            .uri(ws_url)
            .header("Websocket-Id", &session.websocket_id)
            .header("Sec-WebSocket-Protocol", SUBPROTOCOL)
            .header("User-Agent", format!("Stripe/v1 stripe-cli/{}", CLI_VERSION))
             // Add other headers as needed
            .body(())?;


        self.cfg.logger.as_ref().unwrap().debug(&format!("dialing {}", url));

        let (ws_stream, _) = connect_async(request).await?;
        self.cfg.logger.as_ref().unwrap().info("websocket connected");

        let (mut write, mut read) = ws_stream.split();
        let (tx, mut rx) = tokio::sync::mpsc::channel::<Message>(32);
        self.write_tx = Some(tx.clone());

        // Write loop
        let logger_clone = self.cfg.logger.clone().unwrap();
        tokio::spawn(async move {
            while let Some(msg) = rx.recv().await {
                if let Err(e) = write.send(msg).await {
                    logger_clone.error(&format!("write error: {}", e));
                    break;
                }
            }
        });

        // Ping loop
        let tx_clone = tx.clone();
        let ping_period = self.cfg.ping_period.unwrap();
        let logger_ping = self.cfg.logger.clone().unwrap();
        tokio::spawn(async move {
            let mut ticker = interval(ping_period);
            loop {
                ticker.tick().await;
                if let Err(e) = tx_clone.send(Message::Ping(vec![])).await {
                    logger_ping.error(&format!("ping send error: {}", e));
                    break;
                }
                logger_ping.debug("ping sent");
            }
        });

        // Read loop
        let handler = self.cfg.handler.clone();
        let logger_read = self.cfg.logger.clone().unwrap();
        
        // We need to move tx into read loop for ACKs
        let tx_ack = tx.clone();

        while let Some(msg) = read.next().await {
            match msg {
                Ok(Message::Text(text)) => {
                    let incoming: IncomingMessage = match serde_json::from_str(&text) {
                        Ok(v) => v,
                        Err(e) => {
                            logger_read.warn(&format!("malformed message: {}", e));
                            continue;
                        }
                    };

                    match incoming.msg_type.as_str() {
                        "webhook_event" => {
                            if let Ok(evt) = serde_json::from_value::<WebhookEvent>(incoming.data.clone()) {
                                let parsed: StripeEventPayload = match serde_json::from_str(&evt.event_payload) {
                                     Ok(p) => p,
                                     Err(_) => {
                                         logger_read.warn("could not parse event_payload");
                                         continue;
                                     }
                                };
                                
                                // Send ACK
                                let ack = EventAck {
                                    msg_type: "event_ack".to_string(),
                                    event_id: parsed.id.clone(),
                                    webhook_conversation_id: evt.webhook_conversation_id.clone(),
                                    webhook_id: evt.webhook_id.clone(),
                                };
                                if let Ok(ack_json) = serde_json::to_string(&ack) {
                                    let _ = tx_ack.send(Message::Text(ack_json)).await;
                                }

                                handler.on_webhook_event(evt, parsed);
                            }
                        },
                        "v2_event" => {
                             if let Ok(evt) = serde_json::from_value::<V2Event>(incoming.data.clone()) {
                                let parsed: V2EventPayload = match serde_json::from_str(&evt.payload) {
                                     Ok(p) => p,
                                     Err(_) => {
                                         logger_read.warn("could not parse v2 payload");
                                         continue;
                                     }
                                };
                                
                                // Send ACK
                                let ack = EventAck {
                                    msg_type: "event_ack".to_string(),
                                    event_id: parsed.id.clone(),
                                    webhook_conversation_id: "".to_string(),
                                    webhook_id: evt.destination_id.clone(),
                                };
                                if let Ok(ack_json) = serde_json::to_string(&ack) {
                                    let _ = tx_ack.send(Message::Text(ack_json)).await;
                                }

                                handler.on_v2_event(evt, parsed);
                            }
                        },
                        _ => {
                            handler.on_unknown_message(incoming.msg_type, incoming.data);
                        }
                    }
                }
                Ok(Message::Close(_)) => {
                    logger_read.info("websocket closed");
                    break;
                }
                Err(e) => {
                    logger_read.error(&format!("read error: {}", e));
                    break;
                }
                _ => {}
            }
        }

        Ok(())
    }
}
