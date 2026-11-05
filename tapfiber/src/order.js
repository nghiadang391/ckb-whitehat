export function createOrder({ orderId, amountShannons, asset = "CKB", network = "testnet", invoice, expiresAt, paymentHash }) {
  return {
    orderId,
    state: "AWAITING_PAYMENT",
    amountShannons: String(amountShannons),
    asset,
    network,
    invoice,
    paymentHash,
    expiresAt,
  };
}

const keep = (order, rejected) => ({ order, changed: false, ...(rejected ? { rejected } : {}) });

export function applyEvent(order, event) {
  if (order.state !== "AWAITING_PAYMENT") {
    const late = event.type === "payment_confirmed" && order.state !== "PAID";
    return keep(order, late ? `late_confirmation_on_${order.state.toLowerCase()}` : undefined);
  }

  switch (event.type) {
    case "tick":
      return event.now > order.expiresAt ? { order: { ...order, state: "EXPIRED" }, changed: true } : keep(order);

    case "payment_failed":
      return { order: { ...order, state: "FAILED" }, changed: true };

    case "payment_confirmed": {
      if (event.network !== order.network) return keep(order, "network_mismatch");
      if (event.asset !== order.asset) return keep(order, "asset_mismatch");
      if (String(event.amountShannons) !== order.amountShannons) return keep(order, "amount_mismatch");
      if (order.paymentHash && event.paymentHash !== order.paymentHash) return keep(order, "payment_hash_mismatch");
      if (event.at > order.expiresAt) return keep(order, "confirmed_after_expiry");
      return { order: { ...order, state: "PAID", paymentHash: event.paymentHash, paidAt: event.at }, changed: true };
    }

    default:
      return keep(order, "unknown_event");
  }
}
