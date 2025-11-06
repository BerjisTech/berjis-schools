Payments configuration (Stripe, Flutterwave, M‑Pesa)

Set the following environment variables for the Schools service. Leave them empty until you’re ready; the code detects presence before using a gateway.

Stripe
- STRIPE_SECRET_KEY=
- STRIPE_WEBHOOK_SECRET=

Flutterwave
- FLW_SECRET_KEY=
- FLW_PUBLIC_KEY=
- FLW_WEBHOOK_SECRET=  (aka verif-hash if used)

M‑Pesa (Safaricom STK Push)
- MPESA_CONSUMER_KEY=
- MPESA_CONSUMER_SECRET=
- MPESA_SHORT_CODE=
- MPESA_PASSKEY=
- MPESA_CALLBACK_URL=
- MPESA_BASE_URL=   (default: https://sandbox.safaricom.co.ke)

Notes
- Checkout endpoint: POST /v1/payments/checkout with { classId, gateway: 'stripe'|'flutterwave'|'mpesa', currency?, successUrl?, cancelUrl?, phone? }
- Webhooks:
  - Stripe: POST /v1/payments/stripe/webhook
  - Flutterwave: set your webhook to POST to /v1/payments/flutterwave/webhook (to be added if needed; verification may use FLW_WEBHOOK_SECRET)
  - M‑Pesa: configure MPESA_CALLBACK_URL to point to /v1/payments/mpesa/callback on this service (to be added if needed).

