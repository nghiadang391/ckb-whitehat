# TapFiber Core

Small, dependency-free primitives for Fiber payment requests and receipts.

## Modules

- `payload`: encodes and decodes `tapfiber:1` payment requests.
- `order`: applies payment events to an order state machine.
- `receipt`: creates and verifies Ed25519 receipts.

The package has no network or storage layer. A Fiber RPC client, webhook consumer, or other adapter supplies payment events to `applyEvent`.

## Install

```bash
npm test
node bin/tapfiber.js decode <payload>
node bin/tapfiber.js verify <receipt.json> [merchant-public-key.pem]
```

```js
import {
  applyEvent,
  createOrder,
  decodePayload,
  encodePayload,
  issueReceipt,
  verifyReceipt,
} from "tapfiber-core";
```

## Notes

- `payment_confirmed` must match the order's amount, asset, network, payment hash, and expiry.
- Terminal states are immutable; repeated events do not create additional receipts.
- `verifyReceipt` should be called with an expected merchant public key.
- The payload checksum detects corruption. It is not a signature.
- Callers are responsible for persistence and event delivery.
