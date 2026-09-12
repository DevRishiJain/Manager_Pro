# TableOS — Production Hardening & Operational Correctness Specification

**Version:** 1.0.0 (Architecture Frozen)  
**Target:** Restaurant Dining Session Operating System Backend (Go)  
**Date:** September 2026  

---

## 1. Canonical Analytics & Finance Metric Dictionary

To prevent discrepancies across restaurant manager dashboards, scheduled CSV exports, platform super-admin reports, and settlement ledgers, every monetary and operational metric is formally defined below.

| Metric | Formula / Source | Included States | Excluded States | Refund Treatment | Cancellation Treatment | Tax Treatment | Timezone Attribution | Rounding Policy |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **`ORDER_GMV`** | `SUM(order_items.line_total + tax)` from `orders` | `ACCEPTED`, `PREPARING`, `READY`, `SERVED` | `PLACED_UNVERIFIED`, `REJECTED`, `CANCELLED` (Pre-prep) | Excluded (refunds occur post-payment) | If cancelled `POST_PREP_START`, retains 100% GMV | Includes CGST + SGST | Local restaurant timezone date of `orders.placed_at` | Exact integer paise |
| **`PAID_GMV`** | `SUM(payments.amount)` from `payments` | `payments.status = 'CONFIRMED'` | `INITIATED`, `PENDING_CONFIRMATION`, `FAILED` | Does NOT subtract refunds (gross paid) | N/A | Gross inclusive | Local restaurant timezone date of `payments.confirmed_at` | Exact integer paise |
| **`SETTLED_GMV`** | `SUM(sessions.final_total)` from `sessions` | `sessions.status IN ('PAID', 'COMPLETED')` | `OPEN`, `OPEN_VERIFIED`, `AWAITING_PAYMENT`, `EXPIRED`, `WALKOUT`, `FORCE_CLOSED` (unpaid) | Deducts settled refunds | Excluded if whole session voided | Gross inclusive | Local restaurant timezone date of `sessions.closed_at` | Exact integer paise |
| **`REFUNDED_GMV`** | `SUM(refunds.amount)` from `refunds` | `status = 'PROCESSED'` | `INITIATED`, `REJECTED` | Represents total refunded value | N/A | Prorated tax included | Local restaurant timezone date of `refunds.created_at` | Exact integer paise |
| **`PLATFORM_FEE`** | `(session_gmv * commission_rate_bps + 5000) / 10000` | `sessions.status IN ('PAID', 'COMPLETED')` | Unpaid sessions | Reduced via `refund_adjustments` if full/partial refund approved | Retained 100% on `POST_PREP_START` cancellations | Applied on gross GMV before platform fee | Billing period (`YYYY-MM`) based on `created_at` in restaurant local timezone | Round-Half-Up integer paise |
| **`NET_RESTAURANT_AMOUNT`** | `SETTLED_GMV - PLATFORM_FEE - REFUNDED_GMV ± ADJUSTMENTS` | Batch state `SETTLED` | Unsettled batches | Subtracted directly | Subtracted if merchant absorbed | Net of all statutory taxes | Settlement cycle date in restaurant local timezone | Exact integer paise |
| **`AOV`** (Average Order Value) | `ORDER_GMV / total_accepted_orders` | `orders.status = 'SERVED'` | `CANCELLED`, `REJECTED` | Excluded | Excluded | Gross inclusive | Local restaurant date | Integer division: `paise / count` |
| **`SESSION_VALUE`** | `PAID_GMV / total_completed_sessions` | `sessions.status IN ('PAID', 'COMPLETED')` | `EXPIRED`, `WALKOUT` | Net of refunds | Excluded | Gross inclusive | Local restaurant date | Integer division: `paise / count` |
| **`TABLE_UTILIZATION`** | `total_occupied_minutes / total_operating_minutes` | `OPEN`, `OPEN_VERIFIED`, `AWAITING_PAYMENT`, `PAID` | Tables marked `is_active = false` | N/A | N/A | N/A | Operating hours window in restaurant local timezone | Basis points (e.g. 7540 = 75.4%) |
| **`PAYMENT_SUCCESS_RATE`** | `confirmed_payments / (confirmed_payments + failed_payments)` | `CONFIRMED`, `FAILED` | `INITIATED` (abandoned cart) | N/A | N/A | N/A | Payment attempt date in restaurant local timezone | Basis points (e.g. 9850 = 98.5%) |

---

## 2. Automated Financial & Ledger Invariants

The dining engine enforces four immutable mathematical laws across services, handlers, and automated test suites:

### Invariant 1: Bill Breakdown Conservation
$$\text{FinalBill} = \text{Subtotal} + \text{CGST} + \text{SGST} + \text{Adjustments} - \text{Discounts}$$
* Verified in: `AssertBillBreakdownInvariant()` in [`financial_invariants_test.go`](file:///Users/devrishijain/Documents/Projects/TABLE_MANAGER/tests/financial_invariants_test.go).
* Guaranteed by snapshot items: Item additions recalculate running subtotal and taxes atomically under Postgres row locks.

### Invariant 2: Payment Sufficiency & Overpayment Credit
$$\sum \text{ConfirmedPayments} + \text{CreditAdjustments} \ge \text{FinalBill}$$
* When customer payment exceeds `final_bill` (e.g. UPI overpayment or tip split), the surplus is recorded as an immutable `AdjustmentTypeOverpaymentCredit`.
* Session only transitions to `PAID` once total payments satisfy or exceed the final bill.

### Invariant 3: Restaurant Settlement Net Conservation
$$\text{NetDisbursement} = \text{SettledGMV} - \text{PlatformFee} - \text{Refunds} \pm \text{Adjustments}$$
* Verified in: `AssertSettlementLedgerInvariant()`.

### Invariant 4: Deterministic Integer Rounding
$$\text{FeePaise} = \left\lfloor \frac{\text{GMVPaise} \times \text{RateBps} + 5000}{10000} \right\rfloor$$
* Floats are strictly prohibited. Round-half-up integer arithmetic prevents fractional paise leaks.

---

## 3. Idempotency Response Replay & Conflict Semantics

All mutating endpoints (`/session/start`, `/orders`, `/pay`, `/payments/confirm`, `/force-close`) accept an `Idempotency-Key` header.

```
Client Request
      │
      ▼
Check Idempotency-Key
      ├───────────────────────────────┐
      ▼ (Key exists)                  ▼ (New Key)
Compare SHA-256(Body)           Record In-Progress
      ├───────────────┐               │
      ▼ (Identical)   ▼ (Differs)     ▼
Replay Cached     409 Conflict  Execute Handler
Status + Body   IDEMPOTENCY_KEY_REUSED│
+ Headers                             ▼
(X-Cache: IDEMPOTENT-REPLAY)    Cache Status, Body, Headers
```

* **Exact Match Replay**: If the same client retries after network disconnect with identical key + payload, the server returns the cached status code and body byte-for-byte with header `X-Cache: IDEMPOTENT-REPLAY`. Downstream handlers are never executed twice.
* **Payload Mismatch Conflict**: If the same key is reused with a modified payload (e.g., altered table or amount), HTTP `409 Conflict` is returned with `code: IDEMPOTENCY_KEY_REUSED`.

---

## 4. Distributed Locking Strategy & PostgreSQL Authority

```
┌─────────────────────────────────────────────────────────────────┐
│                    POSTGRESQL 16 (AUTHORITY)                   │
│                                                                 │
│  - SELECT ... FOR UPDATE (Row-level exclusive session lock)     │
│  - SERIALIZABLE / READ COMMITTED with optimistic version checks │
│  - Unique Constraints & CHECK Constraints                       │
│  - Row-Level Security (tenant isolation)                        │
└────────────────────────────────┬────────────────────────────────┘
                                 │ Financial / State Authority
                                 ▼
┌─────────────────────────────────────────────────────────────────┐
│                    REDIS (PERFORMANCE CACHE ONLY)               │
│                                                                 │
│  - Rate-limit counters                                          │
│  - Fast table status lookup                                     │
│  - Ephemeral distributed advisory lock (OPTIONAL)               │
│                                                                 │
│  CRITICAL LAW: Redis must NEVER be required for correctness.    │
│  If Redis crashes, Postgres handles 100% of locks & state.      │
└─────────────────────────────────────────────────────────────────┘
```

> **System Law**: PostgreSQL is the sole authority for financial balance and session state. Distributed locks in Redis (if introduced) are advisory performance optimizations to shed database load and must **never** be required for ACID correctness or financial safety.

---

## 5. Transactional Outbox Worker Guarantees

Asynchronous side-effects (SMS OTP, Razorpay refund webhooks, notification events) must never be lost if a worker process crashes or the network partitions.

### Outbox Lifecycle
$$\text{PENDING} \xrightarrow{\text{Claim (Lease)}} \text{CLAIMED} \xrightarrow{\text{Dispatch OK}} \text{PUBLISHED}$$
$$\text{CLAIMED} \xrightarrow[\text{Error}]{\text{Retry } < 5} \text{PENDING (Backoff: } 2^n \times 1\text{s)} \xrightarrow[\text{Retry } \ge 5]{\text{Poison Event}} \text{DEAD\_LETTER}$$

1. **Atomic Claiming with Visibility Lease**:
   ```sql
   UPDATE outbox_events
   SET status = 'CLAIMED',
       claimed_at = NOW(),
       claim_lease_expires_at = NOW() + INTERVAL '30 seconds'
   WHERE id IN (
       SELECT id FROM outbox_events
       WHERE (status = 'PENDING' AND (next_retry_at IS NULL OR next_retry_at <= NOW()))
          OR (status = 'CLAIMED' AND claim_lease_expires_at <= NOW())
       LIMIT 50 FOR UPDATE SKIP LOCKED
   ) RETURNING *;
   ```
2. **Exponential Backoff**: Transient downstream errors back off by $2^n \times 1\text{s}$ ($1\text{s}, 2\text{s}, 4\text{s}, 8\text{s}, 16\text{s}$).
3. **Dead-Letter State (`DEAD_LETTER`)**: After 5 consecutive failures, the event is marked dead-lettered and an ops alert is logged.
4. **Poison-Event Replay**: `ReplayDeadLetterOutbox(ctx, eventID)` resets retries and transitions event back to `PENDING` once the underlying root cause is resolved.

---

## 6. Timezone & Midnight-Crossing Edge-Case Rules

1. **UTC Storage**: All timestamps in PostgreSQL are stored in UTC using `TIMESTAMPTZ`.
2. **Local Business Date Determination**:
   * Every restaurant entity defines `timezone` (e.g. `Asia/Kolkata`, `Asia/Dubai`).
   * When calculating daily reports or financial attribution, timestamps are converted into the restaurant's local timezone: `CreatedAt.In(restaurantLocation).Format("2006-01-02")`.
3. **Midnight-Crossing Sessions**:
   * A table session opened at `23:45 IST` and billed at `00:15 IST` maintains its financial business date attributed to the restaurant's local operating day.
   * In UTC, `00:15 IST` on `2026-09-13` is `18:45 UTC` on `2026-09-12`. The engine explicitly converts using `Asia/Kolkata` so reporting correctly attributes the session to the local business date.

---

## 7. Database Backup & Disaster Recovery (DR)

### Backup Specifications
* **Engine**: PostgreSQL 16 on AWS Aurora / RDS.
* **Point-in-Time Recovery (PITR)**: Continuous Write-Ahead Log (WAL) archiving to encrypted S3 with 35-day retention.
* **Automated Daily Snapshots**: Retained for 90 days.
* **Recovery Point Objective (RPO)**: $\le 5$ minutes (WAL segment archiving).
* **Recovery Time Objective (RTO)**: $\le 30$ minutes (automated failover & snapshot restore).

### S3 Backup Hardening
* **S3 Versioning**: Enabled with Object Lock (Compliance mode) preventing accidental deletion.
* **Lifecycle Policy**: Snapshots transition to Glacier Instant Retrieval after 30 days and expire at 365 days.
* **Encryption**: AWS KMS Customer Managed Keys (CMK) with automatic annual key rotation.
* **Disaster Recovery Drill**: Bi-annual automated restore drill to a standalone staging VPC with data integrity checksum validation.

---

## 8. AWS IAM Credential Hardening

Permanent AWS Access Keys (`AKIA...`) are **strictly prohibited** in production environment files.

```
Development / Local:
  AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY or LocalStack endpoint

AWS Production (EKS / ECS / EC2):
  IAM Role for Service Accounts (IRSA) / EC2 Instance Profile
  Temporary STS credentials assumed automatically via AWS SDK
```

### Least-Privilege IAM Policy Scope
```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "S3PrivateEvidenceAccess",
      "Effect": "Allow",
      "Action": [
        "s3:PutObject",
        "s3:GetObject",
        "s3:AbortMultipartUpload"
      ],
      "Resource": "arn:aws:s3:::tableos-production-evidence/*"
    },
    {
      "Sid": "SNSSendTransactionalOTP",
      "Effect": "Allow",
      "Action": "sns:Publish",
      "Resource": "*"
    }
  ]
}
```

---

## 9. Private S3 Evidence Architecture & Signed URLs

Payment evidence photos (POS receipts, aggregator screenshot vouchers) contain sensitive customer and financial information. They must **never** be publicly accessible.

1. **Database Storage**:
   The database stores structured S3 object metadata:
   * `evidence_bucket`: S3 bucket name
   * `evidence_object_key`: Server-generated UUID path (`payment-proofs/{restaurant_id}/{payment_id}/{uuid}.jpg`)
   * `evidence_content_type`: MIME type (`image/jpeg`, `image/png`, `image/webp`)
   * `evidence_size_bytes`: File size
   * `evidence_sha256`: Cryptographic integrity hash
   * `evidence_uploaded_at`: Upload timestamp
2. **Access Control & Pre-Signed URLs**:
   * Endpoint: `GET /api/v1/restaurant/payments/{id}/evidence-url`
   * Protected by `StaffAuth`: Only authenticated managers/cashiers belonging to that restaurant can request evidence.
   * Generates a temporary pre-signed SigV4 GET URL with **15-minute expiration** ($TTL = 900\text{s}$).

---

## 10. Security Headers & API Abuse Protection

### Production Security Headers
* `Strict-Transport-Security: max-age=63072000; includeSubDomains` (HTTPS only)
* `X-Content-Type-Options: nosniff`
* `X-Frame-Options: DENY`
* `X-XSS-Protection: 1; mode=block`
* `Referrer-Policy: strict-origin-when-cross-origin`

### Abuse & DoS Protection
* **Body Size Limits**: Max 5 MB for evidentiary file uploads; Max 1 MB for JSON payloads.
* **Rate Limits**:
  * Public Session QR start: 60 requests/min per IP.
  * OTP verification attempts: Max 5 failed attempts per session before temporary 15-minute lockout.
  * Payment confirmation attempts: 30 requests/min per table session.
  * Staff endpoints: 300 requests/min per staff token.
* **Payload Verification**: Rejection of SVG/XML or `<script>` tags in file uploads to eliminate XSS vectors.

---

## 11. API Versioning & Routing Convention

All endpoints follow strict semantic versioning prefixed with `/api/v1`:
* Customer: `/api/v1/session/...`
* Staff: `/api/v1/staff/...`
* Kitchen: `/api/v1/kitchen/...`
* Guard: `/api/v1/guard/...`
* Restaurant Admin: `/api/v1/restaurant/...`
* Platform Admin: `/api/v1/admin/...`
* Webhooks: `/api/v1/webhooks/razorpay`

---

## 12. Correlation & Distributed Request Tracing

Every request is assigned a `request_id` and `correlation_id`:
* Response Headers: `X-Request-ID`, `X-Correlation-ID`.
* Context Propagation: Propagated through Chi middleware into Go `context.Context` and structured `slog` attributes.
* Trace Audit: When troubleshooting a discrepancy (e.g. *"Customer paid ₹2,000 but table says unpaid"*), engineers can grep the `request_id` across API router, payment confirmation, outbox dispatcher, and audit logs.

---

## 13. Audit Log Immutability & Redaction Policy

* **Append-Only Immutability**: Audit logs and staff action tables have no `UPDATE` or `DELETE` grants. PostgreSQL triggers reject modifications.
* **Sensitive Field Redaction**:
  * OTPs, JWT secrets, database passwords, Razorpay API secrets, and credit card numbers are strictly barred from log output.
  * Payloads log only masked phone numbers (`+91*****43210`) and transaction references.
* **Retention**: Audit logs are retained for **7 years** in adherence with statutory tax and hospitality compliance regulations.

---

## 14. Graceful Degradation Matrix

| Component Down | Immediate System Impact | Fallback / Mitigation Strategy |
| :--- | :--- | :--- |
| **PostgreSQL** | Fatal (503 Service Unavailable) | Aurora Multi-AZ replica auto-failover within 30 seconds. Fail-closed financial safety. |
| **Redis** | Degraded performance | Engine transparently falls back to direct PostgreSQL row-locking and in-memory rate limiting. No state corruption. |
| **AWS S3** | Image/proof upload fails | Payment record created in `PENDING_CONFIRMATION`; staff cash/POS confirmation falls back to manual physical receipt check until S3 recovers. |
| **AWS SNS** | SMS OTP dispatch delayed | Staff can view the numeric OTP directly on the authenticated POS/Waiter handheld for verbal verification at the table. |
| **Razorpay** | Online payments unavailable | System prompts customer with alternative rails: Direct UPI QR, Table Cash, or Restaurant POS Card terminal. |
| **CloudFront** | Asset loading slow | Static assets served directly from origin S3 fallback bucket. |
| **Outbox Worker** | Async side-effects queued | Events persist safely in PostgreSQL `outbox_events` table; workers resume and drain queue on restart with zero data loss. |

---

## 15. Feature Flags & Configuration Snapshots

Restaurant configuration parameters are snapshotted at session creation:
* `commission_rate_bps`
* `gst_rate_bps`
* `exit_verification_mode` (`EXIT_GUARD_ENABLED` vs `NO_EXIT_VERIFICATION`)
* `shared_session_policy` (`SHARED_TABLE_SESSION` vs `SINGLE_DEVICE_SESSION`)

*Rule*: Changes made to restaurant settings mid-shift only affect newly opened dining sessions. Active sessions preserve their original financial and operational rules snapshot.

---

## 16. Soft Deletion vs Financial Immutability Policy

| Entity | Deletion Policy | Implementation |
| :--- | :--- | :--- |
| **Restaurant** | Soft Deactivate | `status = 'SUSPENDED'` or `status = 'INACTIVE'` |
| **Staff Member** | Soft Deactivate | `is_active = false`; JWT invalidation |
| **Dining Table** | Soft Retire | `is_active = false`; QR token deactivated |
| **Menu Item** | Soft Archive | `is_active = false`; historical order references preserved |
| **Order / OrderItem** | **IMMUTABLE** | Status moves to `CANCELLED`; record is never deleted |
| **Payment** | **IMMUTABLE** | Status moves to `FAILED` or `REFUNDED`; never deleted |
| **Platform Fee Ledger** | **IMMUTABLE** | Append-only ledger; corrected only via `refund_adjustments` |
| **Settlement Batch** | **IMMUTABLE** | Append-only batch record |
| **Audit Log** | **IMMUTABLE** | Append-only; triggers reject `UPDATE` and `DELETE` |

---

## 17. Architecture Freeze Declaration

With the completion of:
1. Automated financial & ledger invariants suite (`financial_invariants_test.go`),
2. Exact idempotency response replay & conflict prevention (`middleware.go`, `hardening_test.go`),
3. Transactional outbox worker leasing, exponential backoff, DLQ & replay (`worker.go`, `memory.go`),
4. Private S3 evidence coordinates and HMAC SigV4 signed URL generation (`s3.go`, `handlers.go`),
5. Local restaurant timezone attribution for midnight-crossing business dates,
6. Complete 16-step simulated production restaurant journey with boundary failure injections (`production_simulated_e2e_test.go`),
7. 93 passing tests with zero race conditions under `go test -race ./...`,

**The TableOS backend architecture is officially frozen.**  
All subsequent engineering moves forward into deployment pipeline configuration, staging environments, real Razorpay sandbox webhooks, and frontend client integration.
