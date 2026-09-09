# Webhook Delivery

Dispatch accepts requests to send webhooks and tracks their eventual delivery.

## Language

**Delivery**:
A durable request to send one webhook to a target URL. It remains the same delivery across retries.
_Avoid_: Job, message, event

**Attempt**:
One outbound HTTP request made for a delivery. A delivery can have multiple attempts.
_Avoid_: Delivery, retry

**Dead letter**:
A delivery that can no longer be retried under its configured retry policy.
_Avoid_: Failed delivery, discarded delivery
