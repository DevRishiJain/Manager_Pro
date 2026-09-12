# 🍽️ Dining OS Backend (Table Manager Pro)

A high-concurrency, ACID-compliant, multi-tenant Restaurant Dining Operating System engineered in Go. Designed to orchestrate live QR table ordering, dynamic bill settlement, Kitchen Display Systems (KDS), guard-gated exit passes, and multi-tenant ledger accounting with mathematical financial precision.

---

## 🌟 Key Architecture & Capabilities

- **Strict Multi-Tenancy & Data Isolation**: Tenant isolation enforced at both the application router layer and via PostgreSQL **Row-Level Security (RLS)** with fail-closed default policies (`app.current_restaurant_id`).
- **QR Table Session Lifecycle**: Complete finite-state machine (`OPEN` → `OPEN_VERIFIED` → `AWAITING_PAYMENT` → `PAID` → `COMPLETED` / `WALKOUT` / `FORCE_CLOSED`).
- **Table Uniqueness Constraint**: Database-enforced partial unique index (`idx_unique_active_session_per_table`) guaranteeing at most one active dining session per physical table.
- **Dynamic 2-Tier Risk Scoring Engine**: Evaluates runaway carts, rapid order jumps (3x multipliers), and velocity spikes. Automatically flags first-time diner orders requiring staff OTP verification before kitchen prep.
- **Kitchen Display System (KDS)**: Real-time order dispatch queue with unidirectional state progression (`PLACED` → `ACCEPTED` → `PREPARING` → `READY` → `SERVED`).
- **Cryptographic Guard Exit Pass**: 4-digit rate-limited numeric OTP with SHA-256 constant-time verification and time-bound JWT QR tokens to eliminate dining walkouts and unpaid departures.
- **Financial Precision & Double-Entry Ledger**:
  - Zero floating-point arithmetic: 100% integer minor units (paise/cents).
  - Indian GST tax calculations (CGST & SGST) using Round-Half-Up rounding.
  - Automatic platform fee calculation (basis points) and running net payable balances.
  - Symmetrical overpayment handling with explicit credit adjustment records.
- **Transactional Outbox Pattern**: Lease-based outbox event worker with exponential backoff and Dead Letter Queue (DLQ) for asynchronous event-driven integrations.
- **Immutable Audit Logging**: Enforced by database triggers disallowing `UPDATE` or `DELETE` on audit trails and high-scrutiny staff action tables.

---

## 🏗️ System Modules & API Reference

The backend provides **37 endpoints across 7 functional modules**:

### 1. Public & Webhooks Module
| Method | Route | Description |
| :--- | :--- | :--- |
| `POST` | `/api/v1/session/start` | Customer scans Table QR token to start dining session & register device |
| `POST` | `/api/v1/webhooks/razorpay` | Asynchronously processes captured gateway payment events with idempotency |

### 2. Customer Dining & Session Module
*Requires opaque customer token (`X-Session-Token` or `Authorization: Bearer <token>`)*
| Method | Route | Description |
| :--- | :--- | :--- |
| `GET` | `/api/v1/session/{id}` | Live session state, active orders, item breakdown, and bill total |
| `POST` | `/api/v1/session/{id}/orders` | Customer places order items with real-time tax calculation and risk evaluation |
| `POST` | `/api/v1/session/{id}/pay` | Requests final bill and initiates payment record (`CASH`, `OWN_GATEWAY`) |
| `GET` | `/api/v1/session/{id}/exit-pass` | Retrieves cryptographically signed exit pass & OTP once bill is paid |

### 3. Staff & Floor Operations Module
*Requires Staff JWT (`WAITER`, `MANAGER`, `RESTAURANT_ADMIN`)*
| Method | Route | Description |
| :--- | :--- | :--- |
| `GET` | `/api/v1/staff/dashboard/tables` | Live table floor overview with active dining session states and occupancy |
| `POST` | `/api/v1/staff/sessions/{id}/verify-first-order` | Staff verifies first order / customer OTP check to advance session to `OPEN_VERIFIED` |
| `POST` | `/api/v1/staff/orders/{id}/accept` | Staff accepts verified order, dispatching ticket to kitchen queue |
| `POST` | `/api/v1/staff/payments/{id}/confirm` | Staff confirms cash payment receipt, creates platform fee ledger entry & issues exit pass |
| `POST` | `/api/v1/staff/payments/confirm` | Body-payload alias for cash/POS payment confirmation |
| `POST` | `/api/v1/staff/sessions/{id}/force-close` | Manager force-closes abandoned/walkout session and logs immutable audit trail |

### 4. Kitchen Display System (KDS) Module
*Requires Staff JWT with role `KITCHEN`, `MANAGER`, or `RESTAURANT_ADMIN`*
| Method | Route | Description |
| :--- | :--- | :--- |
| `GET` | `/api/v1/kitchen/orders/queue` | Active queue of `ACCEPTED` and `PREPARING` food tickets |
| `POST` | `/api/v1/kitchen/orders/{id}/status` | Advances ticket state (`PREPARING` → `READY` → `SERVED`) |

### 5. Security Guard Exit Verification Module
*Requires Guard-Scoped JWT (`GUARD`)*
| Method | Route | Description |
| :--- | :--- | :--- |
| `POST` | `/api/v1/guard/verify-exit` | Validates exit pass QR / OTP in constant time; denies expired/unpaid passes |

### 6. Restaurant Management & Analytics Module
*Requires Staff JWT with role `RESTAURANT_ADMIN`, `RESTAURANT_OWNER`, or `MANAGER`*
| Method | Route | Description |
| :--- | :--- | :--- |
| `GET` | `/api/v1/restaurant/dashboard/overview` | Real-time summary: revenue, active sessions, table turnaround rate |
| `GET` | `/api/v1/restaurant/analytics/today` | Intraday sales, order volume, and ticket averages |
| `GET` | `/api/v1/restaurant/analytics/month-to-date` | Cumulative month-to-date GMV and settled order counts |
| `GET` | `/api/v1/restaurant/analytics/compare` | Period comparison (e.g. today vs yesterday or last week) |
| `GET` | `/api/v1/restaurant/analytics/peak-hours` | Hourly distribution of dining traffic and peak service periods |
| `GET` | `/api/v1/restaurant/analytics/forecast` | Predictive revenue projection based on weighted moving average |
| `GET` | `/api/v1/restaurant/analytics/table-performance` | Turnaround times, GMV contributions, and occupancy per table |
| `GET` | `/api/v1/restaurant/analytics/menu-performance` | Menu item sales velocity and popularity ranking |
| `GET` | `/api/v1/restaurant/ledger` | Platform fee running ledger and platform commission payables |
| `GET` | `/api/v1/restaurant/settlements` | Payout batch history and disbursement statuses |
| `GET` | `/api/v1/restaurant/menu/categories` | List configured menu categories |
| `POST` | `/api/v1/restaurant/menu/categories` | Creates new menu category |
| `GET` | `/api/v1/restaurant/menu/items` | List menu catalog with pricing and taxes |
| `POST` | `/api/v1/restaurant/menu/items` | Adds new item with HSN/SAC code, CGST/SGST rates, and prices |
| `GET` | `/api/v1/restaurant/staff` | List restaurant staff members, roles, and status |
| `GET` | `/api/v1/restaurant/settings` | Operational risk settings, OTP rules, thresholds, policies |
| `PUT` | `/api/v1/restaurant/settings` | Updates operational thresholds and risk parameters |
| `GET` | `/api/v1/restaurant/onboarding` | Full onboarding checklist and completion status |
| `GET` | `/api/v1/restaurant/onboarding/progress` | Calculates onboarding completion percentage and next steps |
| `POST` | `/api/v1/restaurant/onboarding/clone-menu` | Clones starter menu categories and items from industry templates |
| `POST` | `/api/v1/restaurant/onboarding/go-live` | Validates readiness gates and transitions restaurant to `ACTIVE` |
| `POST` | `/api/v1/restaurant/upload-proof` | Uploads offline payment receipt/photo to private object store |
| `GET` | `/api/v1/restaurant/payments/{id}/evidence-url` | Generates 15-minute temporary pre-signed URL to inspect payment proof |

### 7. Platform Super Admin Module
*Requires Platform Super Admin JWT (`is_platform=true`)*
| Method | Route | Description |
| :--- | :--- | :--- |
| `GET` | `/api/v1/admin/restaurants` | Global list of all registered tenant restaurants |
| `GET` | `/api/v1/admin/restaurants/{id}` | Detailed diagnostic profile, table count, active sessions |
| `GET` | `/api/v1/admin/restaurants/{id}/onboarding` | Inspects onboarding checklist and stage for tenant |
| `GET` | `/api/v1/admin/analytics/platform` | Cross-tenant platform GMV and commission collections |
| `GET` | `/api/v1/admin/fraud-review` | Inspects anomaly flags and suspicious walkout patterns |
| `POST` | `/api/v1/admin/restaurants/{id}/commission-rate` | Overrides platform commission rate with audit justification |
| `POST` | `/api/v1/admin/restaurants/{id}/suspend` | Emergency operational freeze / suspension of a restaurant |
| `POST` | `/api/v1/admin/restaurants/{id}/reactivate` | Reactivates suspended restaurant back to `ACTIVE` status |
| `GET` | `/api/v1/admin/restaurants/{id}/tables/qr-export` | Exports table tokens, table numbers, and QR URLs for physical printing |

---

## 📂 Project Structure

```
├── cmd/
│   ├── migrate/        # PostgreSQL schema migration runner
│   ├── server/         # HTTP API server entrypoint & graceful shutdown
│   └── worker/         # Background outbox sweeper & inactivity worker
├── internal/
│   ├── adapter/        # Adapters for Storage (S3/Memory), SMS, Payments, Forecast
│   ├── api/            # Chi HTTP router, handlers, and security middleware
│   │   ├── handlers/   # Unified API handlers for all 7 modules
│   │   └── middleware/ # RLS context, RBAC, customer token & rate limiting
│   ├── config/         # Strongly-typed environment configuration loader
│   ├── domain/         # Core domain models, state machines & invariants
│   │   ├── audit/      # Immutable audit logs & high-scrutiny staff actions
│   │   ├── exitpass/   # Rate-limited OTP and exit pass domain
│   │   ├── ledger/     # Platform fee ledger & settlements
│   │   ├── money/      # Minor-unit integer money & Round-Half-Up math
│   │   ├── order/      # Orders, cart items & kitchen ticket states
│   │   ├── payment/    # Payment state machine, adjustments & methods
│   │   ├── restaurant/ # Tenants, tables, staff, menu & onboarding
│   │   ├── risk/       # Dynamic order risk evaluation engine
│   │   └── session/    # Dining session FSM & state transitions
│   ├── service/        # Service layer orchestrating domain logic
│   └── storage/        # Storage repository interfaces & implementations
│       ├── memory/     # Thread-safe in-memory transactional database
│       └── postgres/   # PostgreSQL migrations and RLS policies
├── pkg/
│   └── crypto/         # JWT generation/parsing, password hashing & secure tokens
└── tests/              # End-to-end integration & concurrency test matrix
```

---

## 🚀 Getting Started

### Prerequisites
- **Go**: 1.22+ installed
- **PostgreSQL**: 16+ (optional for Postgres persistence mode; in-memory storage runs out-of-the-box)

### Setup & Run

1. **Clone the repository**:
   ```bash
   git clone https://github.com/DevRishiJain/Manager_Pro.git
   cd Manager_Pro
   ```

2. **Configure environment**:
   ```bash
   cp .env.example .env
   ```

3. **Run database migrations** *(for local PostgreSQL)*:
   ```bash
   go run cmd/migrate/main.go
   ```

4. **Build binaries**:
   ```bash
   mkdir -p bin
   go build -o bin/server cmd/server/main.go
   go build -o bin/migrate cmd/migrate/main.go
   ```

5. **Start the Dining OS server**:
   ```bash
   ./bin/server
   ```
   The HTTP server will listen on port `8080` (or `PORT` specified in `.env`).

---

## 🧪 Testing & Verification

Run the entire automated test suite, including concurrency race testing, state transition verification, financial invariant checking, and the complete 47-scenario API matrix:

```bash
go test -v -count=1 ./...
```

To run the full end-to-end API Matrix test:
```bash
go test -v -run TestComprehensiveAPIMatrix ./tests
```

To run against real local PostgreSQL with Row Level Security:
```bash
go test -v -run TestPostgres ./tests
```

---

## 🛡️ License

Proprietary / All rights reserved.
