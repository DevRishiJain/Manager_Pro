# TableOS (Dining OS) — Master Demo Guide & Route Inventory

> **Comprehensive Platform Reference, Route Map, and Step-by-Step Presentation Script**  
> *Production-Ready Multi-Tenant QR Dining & Kitchen Orchestration System*

---

## 1. Credentials & Demo Personas Cheat-Sheet

All default staff accounts are pre-seeded in the database with the master password: `password123`.

| Persona / Role | Route | User / Employee ID | Password | Access Capabilities |
| :--- | :--- | :--- | :--- | :--- |
| **Diner (Fine-Dine/Cafe)** | `/t/TBL-001` or `/t/table-qr-token-spice-route-01` | *Self-entry: Name, Phone, Guest Count* | *None (No OTP)* | Table QR dining, menu browsing, Ask AI concierge, cart checkout, order tracking, bill payment, exit pass. |
| **Diner (Drive-In / Car-O-Bar)** | `/t/table-qr-token-drivein-01` | *Self-entry: Car Plate (e.g. DL 01 AB 1234), Name, Phone, Guests* | *None (No OTP)* | Universal static QR, car plate binding, waiter car-hop delivery to vehicle window. |
| **Guest (Hotel Room Service)** | `/t/table-qr-token-hotel-101` | *Self-entry: Name, Phone, Guests (Room 101)* | *None (No OTP)* | In-room dining QR tent card, food delivered directly to hotel room door. |
| **Floor Waiter** | `/staff/login` &rarr; `/staff/orders` | `EMP-WTR-001` | `password123` | Accept placed orders, verify first-order table OTP, pickup ready orders at kitchen pass, mark served. |
| **Kitchen Chef (KDS)** | `/staff/login` &rarr; `/kitchen/queue` | `EMP-CHF-001` | `password123` | 4-Stage Kanban touch display: `ACCEPTED` &rarr; `PREPARING` &rarr; `READY` &rarr; `SERVED`. |
| **Cashier / POS** | `/staff/login` &rarr; `/staff/payments` | `EMP-CSH-001` | `password123` | Confirm pending cash payments and credit card POS swipe settlements. |
| **Exit Security Guard** | `/staff/login` &rarr; `/guard/scan` | `EMP-GRD-001` | `password123` | Scan ExitPass QR code or verify 4-digit departure OTP before letting guests leave. |
| **Restaurant Manager** | `/staff/login` &rarr; `/restaurant/dashboard` | `EMP-MGR-001` | `password123` | Floor plan, force-close tables, active sales, staff shifts. |
| **Tenant Admin / Owner** | `/login` or `/signup` &rarr; `/restaurant/dashboard` | `EMP-ADM-001` or `admin@spiceroute.com` | `password123` | Full tenant control, onboarding wizard, menu management, AI catalog scan, ledger, settlements. |
| **Platform Super Admin** | `/admin/restaurants` | `admin@tableos.internal` (Platform Token) | `password123` | Cross-tenant inspection, commission overrides, suspension, fraud review queue, platform GMV. |

---

## 2. Complete Frontend Route Inventory

### Customer Journey Routes
- `GET /t/[tableToken]` — **QR Entry & Diner Registration**. Asks for Diner Name, Mobile Number, and Party Size (number of guests).
- `GET /dine/[sessionId]/menu` — **Digital Menu & AI Concierge**. Category filtering, item search, customization notes, and instant "Ask AI" drawer.
- `GET /dine/[sessionId]/checkout` — **Cart Review**. Item totals, special instructions, tax preview (2.5% CGST + 2.5% SGST), and order dispatch.
- `GET /dine/[sessionId]/orders` — **Live Order Tracker**. Real-time cooking progress bar (1–5 steps) and prominent first-order table verification code.
- `GET /dine/[sessionId]/bill` — **Itemized Bill & Settlement**. Line item tax breakdown, and payment selection (Gateway, Cash, Card POS).
- `GET /dine/[sessionId]/exit` — **Official Digital Exit Pass**. Boarding-pass UI with dynamic QR code and 4-digit exit code for security gate clearance.

### Operational Staff Routes
- `GET /staff/login` — **Floor Staff Shift Sign-In**. Dedicated portal with one-tap quick profile chips for Waiter, Chef, Cashier, Guard, and Manager.
- `GET /staff/orders` — **Waiter Station**. 3 live tabs: *Pending Acceptance*, *Ready for Pickup (Kitchen Pass)*, and *In-Kitchen Preparation*.
- `GET /staff/tables` — **Live Floor Plan**. Table cards displaying active diner names, contact numbers, party size, running totals, and first-order OTP verification drawer.
- `GET /staff/payments` — **Cashier POS Station**. Settle awaiting cash bills, log partial payments, and issue receipts.
- `GET /kitchen/queue` — **Kitchen Display System (KDS)**. 4-column touch Kanban board with >58px touch buttons for chef ticket progression.
- `GET /guard/scan` — **Security Gate Terminal**. Viewfinder scanner and manual 4-digit OTP verification with full-screen binary pass/fail feedback.

### Tenant Administration & Operations Routes
- `GET /signup` — **Multi-Step Restaurant Onboarding Wizard**. Register legal entity, configure tables & QR codes, seed menu, and assemble staff roster.
- `GET /login` — **Restaurant Portal Login**. Management sign-in for owners and executives.
- `GET /restaurant/dashboard` — **Operations Executive Hub**. Real-time occupancy, live tables count, daily GMV, and active alerts.
- `GET /restaurant/tables` — **Table & QR Studio**. Provision new tables, adjust seating capacity, preview QR codes, and download printable table tent cards.
- `GET /restaurant/menu` — **Menu Studio & AI OCR Ingestion**. Manage categories, create dishes, and batch-import menus from physical photographs.
- `GET /restaurant/staff` — **Staff Roster**. Create new staff accounts with Employee IDs, role assignment, and access passcodes.
- `GET /restaurant/settings` — **Tenant Configuration**. Tax settings, service charge policies, and single-device vs shared table policies.
- `GET /restaurant/ledger` — **Platform Financial Ledger**. Transparent view of gross GMV, platform fees (1%), GST collected, and running net payable.
- `GET /restaurant/settlements` — **Settlements & Payouts**. Payout batches, bank transfer status, and reconciliation logs.
- `GET /restaurant/analytics/today` — **Real-Time Daily Sales Analytics**. Hourly breakdowns, average ticket size, and payment split metrics.
- `GET /restaurant/analytics/forecast` — **Predictive AI Sales Forecasting**. Moving-average projections for inventory planning.
- `GET /restaurant/analytics/peak-hours` — **Traffic Heatmap**. Peak dining hours analysis.
- `GET /restaurant/analytics/menu-performance` — **Menu Item Popularity & Margin Matrix**. Top performers vs slow movers.
- `GET /restaurant/analytics/table-performance` — **Table Turnover & Yield**. Revenue and average dwell time per physical table.
- `GET /restaurant/analytics/compare` — **Period-Over-Period Financial Comparison**.

### Platform Super Admin Routes
- `GET /admin/restaurants` — **Platform Tenant Directory**. Global overview of all enrolled restaurants and live status.
- `GET /admin/restaurants/[id]` — **Tenant Inspector**. Override commission rates, trigger security audits, or suspend accounts.
- `GET /admin/fraud-review` — **High-Risk Transaction Queue**. Review flagged orders and abnormal charge attempts.
- `GET /admin/analytics` — **Global Platform Analytics**. Aggregate GMV across all tenants and platform fee revenue.

---

## 3. End-to-End Step-by-Step Live Demo Script

Follow this sequence for a flawless demo showcasing the entire platform lifecycle:

### Phase 1: The Diner Experience (Mobile Flow)
1. **Scan QR Code & Enter Table Details**:
   - Open `/t/TBL-001` (or click Table 1 on the tables page).
   - Enter your name: `Dev Rishi Jain`, phone: `9876543210`, and select `4 Guests`.
   - Tap **"Start Dining & View Menu"**.
   - *Highlight*: The session is created in the database and linked to Table 1, informing the kitchen of party size for portion planning.
2. **AI Dining Concierge ("Ask AI")**:
   - On the menu screen (`/dine/[sessionId]/menu`), point out the glowing top banner: **"Ask AI Dining Concierge"**.
   - Tap the banner or the gold **"Ask AI"** button.
   - Click the preset chips to demonstrate intelligent grounded reasoning:
     - 🩺 **"What's good for a diabetic patient in sweets at this restaurant?"**  
       *AI Answer*: Warns against high-sugar Gulab Jamun and suggests protein-rich Paneer Tikka or whole-wheat Roti.
     - 💰 **"Create a whole Indian meal under 1000 from the menu"**  
       *AI Answer*: Dynamically curates Starter (Paneer Tikka) + Curry (Dal Makhani) + Breads (Butter Naan) + Dessert (Gulab Jamun) totaling ₹890–₹960 with exact item matches!
     - 🔥 **"What's special here?"**  
       *AI Answer*: Highlights chef signature specials.
   - Tap **"Add"** directly on a recommended dish in the drawer to add it straight to your cart.
3. **Cart & Ordering**:
   - Add **Paneer Tikka (x2)** and **Dal Makhani (x1)** to your cart.
   - Tap **"View Cart"** (`/dine/[sessionId]/checkout`).
   - Add a cooking note: *"Extra crispy paneer, less butter in dal"*.
   - Tap **"Place Order"**.
4. **First-Order Verification**:
   - The screen moves to the order tracker (`/dine/[sessionId]/orders`).
   - Point out the **bold 4-digit Table Verification Code** (e.g., `4819`).
   - Explain: *"This prevents fraudulent orders from outside the restaurant. The order is placed but on hold until floor staff verifies table occupancy."*


### Phase 1B: Drive-In & Car-O-Bar Experience (Vehicle Ordering)
1. **Universal Static QR Scan**:
   - Open `/t/table-qr-token-drivein-01` (or scan the universal Drive-In QR code from any parking bay).
   - Point out the car icon 🚗 and banner: **"Drive-In • Car-O-Bar"**.
   - Enter Car Number Plate: `DL 01 AB 1234` (mandatory for vehicle delivery).
   - Enter Name: `Dev Rishi Jain`, Phone: `9876543210`, and Party Size: `3 Guests`.
   - Tap **"Place Car Order & View Menu"**.
2. **Order Placement & Routing**:
   - Add dishes to cart and place the order.
   - On Kitchen KDS (`/kitchen/queue`): The ticket immediately appears with a bold amber badge **🚗 Car DL 01 AB 1234** and party count.
   - On Waiter Station (`/staff/orders`): Waiters see: **"Deliver to Dev Rishi Jain at Car DL 01 AB 1234 (3 Guests)"**.
   - When food is ready at pass, the waiter button states: **"Mark Delivered to Car ✅"**.

### Phase 1C: Hotel In-Room Dining Experience
1. **In-Room QR Scan**:
   - Open `/t/table-qr-token-hotel-101` (representing Room 101 tent card).
   - Notice the purple hotel badge **🏨 Room 101 • In-Room Dining**.
   - Place order; KDS and Waiter station route the ticket directly to **Room 101** with **"Deliver to Room 101 ✅"**.

### Phase 2: Floor Waiter Flow
1. Open `/staff/login` in another browser window or tab.
2. Click the quick role chip **"Waiter Terminal"** (`EMP-WTR-001` / `password123`) and sign in.
3. Open **Live Floor Plan** (`/staff/tables`):
   - Table 1 shows as **"Unverified (Enter OTP)"**, displaying `4 Guests • Dev Rishi Jain • 9876543210`.
   - Click Table 1, enter the customer's 4-digit code in the drawer, and tap **"Verify"**.
   - Table 1 turns green: **"Active Dining"**.
4. Switch to **Orders Station** (`/staff/orders`):
   - The ticket appears under **"Pending Acceptance"**.
   - Tap **"Accept & Route to Kitchen"**. The ticket vanishes from pending and moves to the kitchen ticket feed.

### Phase 3: Kitchen Display System (KDS)
1. In another tab, open `/kitchen/queue` (or sign in as `EMP-CHF-001`).
2. Point out the 4-column touch Kanban board:
   - Order #1 is under **"Accepted / New"**.
   - Tap the large button: **"Start Cooking 🔥"**. The ticket moves to **"Actively Cooking"** (`PREPARING`).
   - When food is ready, tap: **"Mark Ready on Pass 🛎️"**. The ticket moves to **"Ready for Pickup"** (`READY`).

### Phase 4: Waiter Pickup & Serving
1. Switch back to the Waiter Station (`/staff/orders`).
2. Click the tab: **"Ready for Pickup (1)"**.
3. Table 1's order is highlighted in bright emerald with a bell: *"Ready at pass"*.
4. Waiter collects the plates from the kitchen and taps **"Mark Served to Table"**.
5. Customer's order tracker immediately updates to **"Served"** with 5/5 progress bars filled!

### Phase 5: Bill Payment & Departure
1. On the customer's phone, tap **"View Full Bill"** (`/dine/[sessionId]/bill`).
2. Point out the itemized receipt:
   - Items Subtotal: `₹1,020.00`
   - CGST (2.5%): `₹25.50`
   - SGST (2.5%): `₹25.50`
   - Grand Total: `₹1,071.00`
3. Select **"Cash at Table"** (or Online Gateway) and tap **"Pay & Settle"**.
4. Cashier confirms settlement at `/staff/payments`.
5. Customer bill instantly turns green: **"Payment Completed!"**.
6. Tap **"Generate Exit Pass"** (`/dine/[sessionId]/exit`):
   - Displays the cryptographic Exit Pass boarding pass with high-density QR code and 4-digit departure code.

### Phase 6: Exit Security Gate
1. Open `/guard/scan` (or sign in as `EMP-GRD-001`).
2. Enter the Session ID and 4-digit departure code.
3. Tap **"Verify Exit Pass"**:
   - Screen turns full-screen emerald: **"PASS APPROVED — Guest Authorized to Depart"**.
4. Tap **"Scan Next Guest"** to reset.

### Phase 7: Management & Financial Reconciliation
1. Sign in as Owner/Admin (`EMP-ADM-001` or `/restaurant/dashboard`).
2. **Dashboard Overview**: Live occupancy counter updates, today's sales show real GMV, and average order turnaround metrics display.
3. **Financial Ledger** (`/restaurant/ledger`): Transparent breakdown showing Gross Sales, Platform Fee (1%), and Net Settled Funds.
4. **Table QR Studio** (`/restaurant/tables`): Add a new table, adjust seats, and click to view/print high-res QR codes.

---

## 4. Standalone AI Prompt Evaluation Runner

You can also demonstrate the AI engine independently from the terminal using Python:

```bash
python3 scripts/ai_dining_prompts.py
```

This runs automated test queries against the restaurant menu and prints structured JSON answers with matched dish arrays.

---

## 5. Summary of Architecture Highlights to Mention
- **Zero Heavy Payloads**: Paginated and lightweight delta responses designed for busy 4G/5G mobile dining rooms.
- **Double-Entry Financial Ledger**: Immutable money amounts in integer minor units (paise) with standard Half-Up rounding.
- **Optimistic Concurrency**: Eliminates state collisions across concurrent table joins and split bills.
- **Dual AI Mode**: Online Google Gemini multimodal understanding + instant offline deterministic culinary reasoning fallback.
