import { StripeListener, type EventHandler, type WebhookEvent, type V2Event, type StripeEventPayload, type V2EventPayload } from "../src/index.js";

// ── Handler: just log everything ──────────────────────────────

const handler: EventHandler = {
  onWebhookEvent(evt: WebhookEvent, parsed: StripeEventPayload) {
    console.log(`\n──── ${parsed.type} [${parsed.id}] ────`);
    console.log(JSON.stringify(JSON.parse(evt.event_payload), null, 2));
  },

  onV2Event(evt: V2Event, parsed: V2EventPayload) {
    console.log(`\n──── V2 ${parsed.type} [${parsed.id}] ────`);
    console.log(JSON.stringify(JSON.parse(evt.payload), null, 2));
  },

  onUnknownMessage(rawType: string, data: unknown) {
    console.log(`\n──── UNKNOWN type=${rawType} ────`);
    console.log(JSON.stringify(data, null, 2));
  },
};

// ── Logger ────────────────────────────────────────────────────

const logger = {
  debug: (msg: string) => console.log(`[DEBUG] ${msg}`),
  info: (msg: string) => console.log(`[INFO]  ${msg}`),
  warn: (msg: string) => console.log(`[WARN]  ${msg}`),
  error: (msg: string) => console.log(`[ERROR] ${msg}`),
};

// ── Main ──────────────────────────────────────────────────────

const apiKey = process.env.STRIPE_API_KEY;
if (!apiKey) {
  console.error("export STRIPE_API_KEY=sk_test_...");
  process.exit(1);
}

const listener = new StripeListener({
  apiKey,
  handler,
  logger,
});

// Graceful shutdown
const ac = new AbortController();
process.on("SIGINT", () => ac.abort());
process.on("SIGTERM", () => ac.abort());

console.log("Starting Stripe webhook listener...\n");

listener.listen(ac.signal)
  .then(() => console.log("\nDone."))
  .catch((err) => {
    if (err.name !== "AbortError") console.error(err);
  });