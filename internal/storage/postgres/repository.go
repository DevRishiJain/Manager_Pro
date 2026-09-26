package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/audit"
	"github.com/devrishijain/table-manager/internal/domain/exitpass"
	"github.com/devrishijain/table-manager/internal/domain/ledger"
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/order"
	"github.com/devrishijain/table-manager/internal/domain/payment"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
	mem  *memory.MemoryRepository
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{
		pool: pool,
		mem:  memory.NewMemoryRepository(),
	}
}

// ---------------- Session ----------------

func (r *PostgresRepository) CreateSession(ctx context.Context, s *session.DiningSession) error {
	_ = r.mem.CreateSession(ctx, s)
	if r.pool != nil {
		curr := s.RunningTotal.Currency
		if curr == "" {
			curr = "INR"
		}
		openedAt := s.OpenedAt
		if openedAt.IsZero() {
			openedAt = time.Now().UTC()
		}
		lastAct := s.LastActivityAt
		if lastAct.IsZero() {
			lastAct = openedAt
		}
		expiry := s.ExpiryDeadline
		if expiry.IsZero() {
			expiry = openedAt.Add(3 * time.Hour)
		}
		_, err := r.pool.Exec(ctx, `
			INSERT INTO dining_sessions (id, restaurant_id, table_id, status, opened_at, running_total_minor, final_total_minor, platform_fee_minor, currency, session_token, device_fingerprint, last_activity_at, expiry_deadline, version, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
			ON CONFLICT (id) DO NOTHING;
		`, s.ID, s.RestaurantID, s.TableID, string(s.Status), openedAt, s.RunningTotal.AmountMinorUnits, s.FinalTotal.AmountMinorUnits, s.PlatformFeeAmount.AmountMinorUnits, curr, s.SessionToken, s.DeviceFingerprint, lastAct, expiry, s.Version, s.CreatedAt, s.UpdatedAt)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *PostgresRepository) GetSessionByID(ctx context.Context, id uuid.UUID) (*session.DiningSession, error) {
	if r.pool != nil {
		var s session.DiningSession
		var statusStr, curr string
		var runMinor, finMinor, feeMinor int64
		err := r.pool.QueryRow(ctx, `
			SELECT id, restaurant_id, table_id, status, opened_at, running_total_minor, final_total_minor, platform_fee_minor, currency, session_token, device_fingerprint, last_activity_at, expiry_deadline, version, created_at, updated_at
			FROM dining_sessions
			WHERE id = $1;
		`, id).Scan(&s.ID, &s.RestaurantID, &s.TableID, &statusStr, &s.OpenedAt, &runMinor, &finMinor, &feeMinor, &curr, &s.SessionToken, &s.DeviceFingerprint, &s.LastActivityAt, &s.ExpiryDeadline, &s.Version, &s.CreatedAt, &s.UpdatedAt)
		if err == nil {
			s.Status = session.State(statusStr)
			s.RunningTotal = money.New(runMinor)
			s.FinalTotal = money.New(finMinor)
			s.PlatformFeeAmount = money.New(feeMinor)
			return &s, nil
		}
	}
	return r.mem.GetSessionByID(ctx, id)
}

func (r *PostgresRepository) GetSessionByToken(ctx context.Context, token string) (*session.DiningSession, error) {
	if r.pool != nil {
		var s session.DiningSession
		var statusStr, curr string
		var runMinor, finMinor, feeMinor int64
		err := r.pool.QueryRow(ctx, `
			SELECT id, restaurant_id, table_id, status, opened_at, running_total_minor, final_total_minor, platform_fee_minor, currency, session_token, device_fingerprint, last_activity_at, expiry_deadline, version, created_at, updated_at
			FROM dining_sessions
			WHERE session_token = $1;
		`, token).Scan(&s.ID, &s.RestaurantID, &s.TableID, &statusStr, &s.OpenedAt, &runMinor, &finMinor, &feeMinor, &curr, &s.SessionToken, &s.DeviceFingerprint, &s.LastActivityAt, &s.ExpiryDeadline, &s.Version, &s.CreatedAt, &s.UpdatedAt)
		if err == nil {
			s.Status = session.State(statusStr)
			s.RunningTotal = money.New(runMinor)
			s.FinalTotal = money.New(finMinor)
			s.PlatformFeeAmount = money.New(feeMinor)
			return &s, nil
		}
	}
	return r.mem.GetSessionByToken(ctx, token)
}

func (r *PostgresRepository) GetActiveSessionByTableID(ctx context.Context, tableID uuid.UUID) (*session.DiningSession, error) {
	if r.pool != nil {
		var s session.DiningSession
		var statusStr, curr string
		var runMinor, finMinor, feeMinor int64
		err := r.pool.QueryRow(ctx, `
			SELECT id, restaurant_id, table_id, status, opened_at, running_total_minor, final_total_minor, platform_fee_minor, currency, session_token, device_fingerprint, last_activity_at, expiry_deadline, version, created_at, updated_at
			FROM dining_sessions
			WHERE table_id = $1 AND status IN ('OPEN', 'OPEN_VERIFIED', 'AWAITING_PAYMENT')
			LIMIT 1;
		`, tableID).Scan(&s.ID, &s.RestaurantID, &s.TableID, &statusStr, &s.OpenedAt, &runMinor, &finMinor, &feeMinor, &curr, &s.SessionToken, &s.DeviceFingerprint, &s.LastActivityAt, &s.ExpiryDeadline, &s.Version, &s.CreatedAt, &s.UpdatedAt)
		if err == nil {
			s.Status = session.State(statusStr)
			s.RunningTotal = money.New(runMinor)
			s.FinalTotal = money.New(finMinor)
			s.PlatformFeeAmount = money.New(feeMinor)
			return &s, nil
		}
	}
	return r.mem.GetActiveSessionByTableID(ctx, tableID)
}

func (r *PostgresRepository) UpdateSession(ctx context.Context, s *session.DiningSession) error {
	_ = r.mem.UpdateSession(ctx, s)
	if r.pool != nil {
		_, _ = r.pool.Exec(ctx, `
			UPDATE dining_sessions 
			SET status = $1, running_total_minor = $2, final_total_minor = $3, platform_fee_minor = $4, version = $5, updated_at = $6 
			WHERE id = $7;
		`, string(s.Status), s.RunningTotal.AmountMinorUnits, s.FinalTotal.AmountMinorUnits, s.PlatformFeeAmount.AmountMinorUnits, s.Version, s.UpdatedAt, s.ID)
	}
	return nil
}


func (r *PostgresRepository) ListActiveSessions(ctx context.Context, restaurantID uuid.UUID) ([]session.DiningSession, error) {
	return r.mem.ListActiveSessions(ctx, restaurantID)
}

func (r *PostgresRepository) AddParticipant(ctx context.Context, p *session.SessionParticipant) error {
	_ = r.mem.AddParticipant(ctx, p)
	if r.pool != nil {
		_, _ = r.pool.Exec(ctx, `
			INSERT INTO session_participants (id, session_id, device_token, display_name, joined_at)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (id) DO NOTHING;
		`, p.ID, p.SessionID, p.DeviceToken, p.DisplayName, p.JoinedAt)
	}
	return nil
}

func (r *PostgresRepository) GetParticipants(ctx context.Context, sessionID uuid.UUID) ([]session.SessionParticipant, error) {
	return r.mem.GetParticipants(ctx, sessionID)
}

// ---------------- Order ----------------

func (r *PostgresRepository) CreateOrder(ctx context.Context, o *order.Order, items []order.OrderItem) error {
	_ = r.mem.CreateOrder(ctx, o, items)
	if r.pool != nil {
		curr := o.Total.Currency
		if curr == "" {
			curr = "INR"
		}
		placedAt := o.PlacedAt
		if placedAt.IsZero() {
			placedAt = time.Now().UTC()
		}
		tableNum := o.TableNumber
		if tableNum == "" {
			tableNum = "Table"
		}

		type compactItem struct {
			ID                  uuid.UUID `json:"id"`
			MenuItemID          uuid.UUID `json:"menu_item_id"`
			ItemNameSnapshot    string    `json:"item_name_snapshot"`
			Quantity            int       `json:"quantity"`
			UnitPriceMinor      int64     `json:"unit_price_minor"`
			LineTotalMinor      int64     `json:"line_total_minor"`
			SpecialInstructions string    `json:"special_instructions,omitempty"`
		}
		compactItems := make([]compactItem, len(items))
		for i, it := range items {
			compactItems[i] = compactItem{
				ID:                  it.ID,
				MenuItemID:          it.MenuItemID,
				ItemNameSnapshot:    it.ItemNameSnapshot,
				Quantity:            it.Quantity,
				UnitPriceMinor:      it.UnitPriceSnapshot.AmountMinorUnits,
				LineTotalMinor:      it.LineTotal.AmountMinorUnits,
				SpecialInstructions: it.SpecialInstructions,
			}
		}
		summaryJSON, _ := json.Marshal(compactItems)

		_, err := r.pool.Exec(ctx, `
			INSERT INTO orders (
				id, session_id, restaurant_id, sequence_number, status, placed_at, 
				subtotal_minor, tax_total_minor, total_minor, currency, 
				cancellation_fee_applicable, version, created_at, updated_at,
				table_number, items_summary
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
			ON CONFLICT (id) DO NOTHING;
		`, o.ID, o.SessionID, o.RestaurantID, o.SequenceNumber, string(o.Status), placedAt, o.Subtotal.AmountMinorUnits, o.TaxTotal.AmountMinorUnits, o.Total.AmountMinorUnits, curr, o.CancellationFeeApplicable, o.Version, o.CreatedAt, o.UpdatedAt, tableNum, summaryJSON)
		if err != nil {
			return err
		}

		for _, it := range items {
			itemCurr := it.LineTotal.Currency
			if itemCurr == "" {
				itemCurr = curr
			}
			hsn := it.HSNSACCodeSnapshot
			if hsn == "" {
				hsn = "996331"
			}
			_, err = r.pool.Exec(ctx, `
				INSERT INTO order_items (id, order_id, menu_item_id, variant_id, item_name_snapshot, quantity, unit_price_minor, line_total_minor, currency, hsn_sac_code_snapshot, cgst_rate_bps_snapshot, sgst_rate_bps_snapshot, cgst_amount_minor, sgst_amount_minor, special_instructions, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
				ON CONFLICT (id) DO NOTHING;
			`, it.ID, it.OrderID, it.MenuItemID, it.VariantID, it.ItemNameSnapshot, it.Quantity, it.UnitPriceSnapshot.AmountMinorUnits, it.LineTotal.AmountMinorUnits, itemCurr, hsn, it.CGSTRateBpsSnapshot, it.SGSTRateBpsSnapshot, it.CGSTAmount.AmountMinorUnits, it.SGSTAmount.AmountMinorUnits, it.SpecialInstructions, it.CreatedAt)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *PostgresRepository) GetOrderByID(ctx context.Context, id uuid.UUID) (*order.Order, error) {
	if r.pool != nil {
		var o order.Order
		var statusStr, curr string
		var subMinor, taxMinor, totMinor int64
		var accAt, cancAt *time.Time
		var accBy *uuid.UUID
		var cancStageStr *string
		err := r.pool.QueryRow(ctx, `
			SELECT id, session_id, restaurant_id, sequence_number, status, placed_at, accepted_at, accepted_by_staff_id, subtotal_minor, tax_total_minor, total_minor, currency, cancelled_at, cancellation_stage, cancellation_fee_applicable, version, created_at, updated_at
			FROM orders
			WHERE id = $1;
		`, id).Scan(&o.ID, &o.SessionID, &o.RestaurantID, &o.SequenceNumber, &statusStr, &o.PlacedAt, &accAt, &accBy, &subMinor, &taxMinor, &totMinor, &curr, &cancAt, &cancStageStr, &o.CancellationFeeApplicable, &o.Version, &o.CreatedAt, &o.UpdatedAt)
		if err == nil {
			o.Status = order.State(statusStr)
			o.Subtotal = money.New(subMinor)
			o.TaxTotal = money.New(taxMinor)
			o.Total = money.New(totMinor)
			o.AcceptedAt = accAt
			o.AcceptedByStaffID = accBy
			o.CancelledAt = cancAt
			if cancStageStr != nil {
				stage := order.CancellationStage(*cancStageStr)
				o.CancellationStage = &stage
			}

			// Load items
			rows, itemErr := r.pool.Query(ctx, `
				SELECT id, order_id, menu_item_id, variant_id, item_name_snapshot, quantity, unit_price_minor, line_total_minor, currency, hsn_sac_code_snapshot, cgst_rate_bps_snapshot, sgst_rate_bps_snapshot, cgst_amount_minor, sgst_amount_minor, special_instructions, created_at
				FROM order_items
				WHERE order_id = $1;
			`, id)
			if itemErr == nil {
				defer rows.Close()
				for rows.Next() {
					var it order.OrderItem
					var itCurr string
					var unitMinor, lineMinor, cgstMinor, sgstMinor int64
					if scanErr := rows.Scan(&it.ID, &it.OrderID, &it.MenuItemID, &it.VariantID, &it.ItemNameSnapshot, &it.Quantity, &unitMinor, &lineMinor, &itCurr, &it.HSNSACCodeSnapshot, &it.CGSTRateBpsSnapshot, &it.SGSTRateBpsSnapshot, &cgstMinor, &sgstMinor, &it.SpecialInstructions, &it.CreatedAt); scanErr == nil {
						it.UnitPriceSnapshot = money.New(unitMinor)
						it.LineTotal = money.New(lineMinor)
						it.CGSTAmount = money.New(cgstMinor)
						it.SGSTAmount = money.New(sgstMinor)
						o.Items = append(o.Items, it)
					}
				}
			}
			return &o, nil
		}

	}
	return r.mem.GetOrderByID(ctx, id)
}

func (r *PostgresRepository) GetOrdersBySessionID(ctx context.Context, sessionID uuid.UUID) ([]order.Order, error) {
	if r.pool != nil {
		rows, err := r.pool.Query(ctx, `
			SELECT 
				o.id,
				o.session_id,
				o.restaurant_id,
				o.sequence_number,
				o.status,
				o.placed_at,
				o.accepted_at,
				o.accepted_by_staff_id,
				o.subtotal_minor,
				o.tax_total_minor,
				o.total_minor,
				o.version,
				COALESCE(o.items_summary, '[]'::jsonb) AS items_json
			FROM orders o
			WHERE o.session_id = $1
			ORDER BY o.sequence_number ASC;
		`, sessionID)
		if err == nil {
			defer rows.Close()
			var orders []order.Order
			for rows.Next() {
				var o order.Order
				var subMinor, taxMinor, totMinor int64
				var statusStr string
				var itemsJSON []byte

				if err := rows.Scan(
					&o.ID,
					&o.SessionID,
					&o.RestaurantID,
					&o.SequenceNumber,
					&statusStr,
					&o.PlacedAt,
					&o.AcceptedAt,
					&o.AcceptedByStaffID,
					&subMinor,
					&taxMinor,
					&totMinor,
					&o.Version,
					&itemsJSON,
				); err == nil {
					o.Status = order.State(statusStr)
					o.Subtotal = money.New(subMinor)
					o.TaxTotal = money.New(taxMinor)
					o.Total = money.New(totMinor)

					type rawItem struct {
						ID                  uuid.UUID `json:"id"`
						OrderID             uuid.UUID `json:"order_id"`
						MenuItemID          uuid.UUID `json:"menu_item_id"`
						ItemNameSnapshot    string    `json:"item_name_snapshot"`
						Quantity            int       `json:"quantity"`
						UnitPriceMinor      int64     `json:"unit_price_minor"`
						LineTotalMinor      int64     `json:"line_total_minor"`
						SpecialInstructions string    `json:"special_instructions"`
					}
					var rawItems []rawItem
					if err := json.Unmarshal(itemsJSON, &rawItems); err == nil {
						for _, it := range rawItems {
							o.Items = append(o.Items, order.OrderItem{
								ID:                  it.ID,
								OrderID:             it.OrderID,
								MenuItemID:          it.MenuItemID,
								ItemNameSnapshot:    it.ItemNameSnapshot,
								Quantity:            it.Quantity,
								UnitPriceSnapshot:   money.New(it.UnitPriceMinor),
								LineTotal:           money.New(it.LineTotalMinor),
								SpecialInstructions: it.SpecialInstructions,
							})
						}
					}
					orders = append(orders, o)
				}
			}
			if len(orders) > 0 {
				return orders, nil
			}
		}
	}
	return r.mem.GetOrdersBySessionID(ctx, sessionID)
}

func (r *PostgresRepository) UpdateOrder(ctx context.Context, o *order.Order) error {
	_ = r.mem.UpdateOrder(ctx, o)
	if r.pool != nil {
		var validStaffID *uuid.UUID = o.AcceptedByStaffID
		if validStaffID != nil && *validStaffID != uuid.Nil {
			var exists bool
			err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM staff_users WHERE id = $1)`, *validStaffID).Scan(&exists)
			if err != nil || !exists {
				validStaffID = nil
			}
		} else {
			validStaffID = nil
		}

		updatedAt := o.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = time.Now().UTC()
		}

		_, err := r.pool.Exec(ctx, `
			UPDATE orders 
			SET status = $1, accepted_at = $2, accepted_by_staff_id = $3, cancelled_at = $4, cancellation_stage = $5, cancellation_fee_applicable = $6, version = $7, updated_at = $8 
			WHERE id = $9;
		`, string(o.Status), o.AcceptedAt, validStaffID, o.CancelledAt, o.CancellationStage, o.CancellationFeeApplicable, o.Version, updatedAt, o.ID)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *PostgresRepository) ListKitchenQueue(ctx context.Context, restaurantID uuid.UUID, statuses []order.State) ([]order.Order, error) {
	if r.pool != nil {
		statusStrs := make([]string, len(statuses))
		for i, s := range statuses {
			statusStrs[i] = string(s)
		}
		rows, err := r.pool.Query(ctx, `
			SELECT 
				o.id,
				o.session_id,
				o.restaurant_id,
				o.sequence_number,
				o.status,
				o.placed_at,
				o.accepted_at,
				o.accepted_by_staff_id,
				o.subtotal_minor,
				o.tax_total_minor,
				o.total_minor,
				o.version,
				COALESCE(NULLIF(o.table_number, ''), 'Table') AS table_number,
				COALESCE(o.items_summary, '[]'::jsonb) AS items_json
			FROM orders o
			WHERE o.restaurant_id = $1 
			  AND o.status = ANY($2)
			  AND o.placed_at >= NOW() - INTERVAL '12 hours'
			ORDER BY o.sequence_number ASC
			LIMIT 100;
		`, restaurantID, statusStrs)
		if err == nil {
			defer rows.Close()
			var orders []order.Order
			for rows.Next() {
				var o order.Order
				var subMinor, taxMinor, totMinor int64
				var statusStr string
				var itemsJSON []byte

				if err := rows.Scan(
					&o.ID,
					&o.SessionID,
					&o.RestaurantID,
					&o.SequenceNumber,
					&statusStr,
					&o.PlacedAt,
					&o.AcceptedAt,
					&o.AcceptedByStaffID,
					&subMinor,
					&taxMinor,
					&totMinor,
					&o.Version,
					&o.TableNumber,
					&itemsJSON,
				); err == nil {
					o.Status = order.State(statusStr)
					o.Subtotal = money.New(subMinor)
					o.TaxTotal = money.New(taxMinor)
					o.Total = money.New(totMinor)

					type rawItem struct {
						ID                  uuid.UUID `json:"id"`
						OrderID             uuid.UUID `json:"order_id"`
						MenuItemID          uuid.UUID `json:"menu_item_id"`
						ItemNameSnapshot    string    `json:"item_name_snapshot"`
						Quantity            int       `json:"quantity"`
						UnitPriceMinor      int64     `json:"unit_price_minor"`
						LineTotalMinor      int64     `json:"line_total_minor"`
						SpecialInstructions string    `json:"special_instructions"`
					}
					var rawItems []rawItem
					if err := json.Unmarshal(itemsJSON, &rawItems); err == nil {
						for _, it := range rawItems {
							o.Items = append(o.Items, order.OrderItem{
								ID:                  it.ID,
								OrderID:             it.OrderID,
								MenuItemID:          it.MenuItemID,
								ItemNameSnapshot:    it.ItemNameSnapshot,
								Quantity:            it.Quantity,
								UnitPriceSnapshot:   money.New(it.UnitPriceMinor),
								LineTotal:           money.New(it.LineTotalMinor),
								SpecialInstructions: it.SpecialInstructions,
							})
						}
					}
					orders = append(orders, o)
				}
			}
			return orders, nil
		}
	}
	return r.mem.ListKitchenQueue(ctx, restaurantID, statuses)
}

func (r *PostgresRepository) ListPendingOrders(ctx context.Context, restaurantID uuid.UUID) ([]order.Order, error) {
	if r.pool != nil {
		rows, err := r.pool.Query(ctx, `
			SELECT 
				o.id,
				o.session_id,
				o.restaurant_id,
				o.sequence_number,
				o.status,
				o.placed_at,
				o.subtotal_minor,
				o.tax_total_minor,
				o.total_minor,
				o.version,
				COALESCE(NULLIF(o.table_number, ''), 'Table') AS table_number,
				COALESCE(o.items_summary, '[]'::jsonb) AS items_json
			FROM orders o
			WHERE o.restaurant_id = $1 
			  AND o.status IN ('PLACED_UNVERIFIED', 'PLACED_VERIFIED')
			  AND o.placed_at >= NOW() - INTERVAL '12 hours'
			ORDER BY o.placed_at ASC
			LIMIT 100;
		`, restaurantID)
		if err == nil {
			defer rows.Close()
			var orders []order.Order
			for rows.Next() {
				var o order.Order
				var subMinor, taxMinor, totMinor int64
				var statusStr string
				var itemsJSON []byte

				if err := rows.Scan(
					&o.ID,
					&o.SessionID,
					&o.RestaurantID,
					&o.SequenceNumber,
					&statusStr,
					&o.PlacedAt,
					&subMinor,
					&taxMinor,
					&totMinor,
					&o.Version,
					&o.TableNumber,
					&itemsJSON,
				); err == nil {
					o.Status = order.State(statusStr)
					o.Subtotal = money.New(subMinor)
					o.TaxTotal = money.New(taxMinor)
					o.Total = money.New(totMinor)

					type rawItem struct {
						ID                  uuid.UUID `json:"id"`
						OrderID             uuid.UUID `json:"order_id"`
						MenuItemID          uuid.UUID `json:"menu_item_id"`
						ItemNameSnapshot    string    `json:"item_name_snapshot"`
						Quantity            int       `json:"quantity"`
						UnitPriceMinor      int64     `json:"unit_price_minor"`
						LineTotalMinor      int64     `json:"line_total_minor"`
						SpecialInstructions string    `json:"special_instructions"`
					}
					var rawItems []rawItem
					if err := json.Unmarshal(itemsJSON, &rawItems); err == nil {
						for _, it := range rawItems {
							o.Items = append(o.Items, order.OrderItem{
								ID:                  it.ID,
								OrderID:             it.OrderID,
								MenuItemID:          it.MenuItemID,
								ItemNameSnapshot:    it.ItemNameSnapshot,
								Quantity:            it.Quantity,
								UnitPriceSnapshot:   money.New(it.UnitPriceMinor),
								LineTotal:           money.New(it.LineTotalMinor),
								SpecialInstructions: it.SpecialInstructions,
							})
						}
					}
					orders = append(orders, o)
				}
			}
			return orders, nil
		}
	}
	return r.mem.ListPendingOrders(ctx, restaurantID)
}

// ---------------- Payment ----------------

func (r *PostgresRepository) CreatePayment(ctx context.Context, p *payment.Payment) error {
	_ = r.mem.CreatePayment(ctx, p)
	if r.pool != nil {
		_, _ = r.pool.Exec(ctx, `
			INSERT INTO payments (id, session_id, restaurant_id, method, amount_minor, currency, status, version, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (id) DO NOTHING;
		`, p.ID, p.SessionID, p.RestaurantID, string(p.Method), p.Amount.AmountMinorUnits, p.Amount.Currency, p.Status, p.Version, p.CreatedAt, p.UpdatedAt)
	}
	return nil
}

func (r *PostgresRepository) GetPaymentByID(ctx context.Context, id uuid.UUID) (*payment.Payment, error) {
	if r.pool != nil {
		var p payment.Payment
		var methStr, statusStr, curr string
		var amtMinor int64
		err := r.pool.QueryRow(ctx, `
			SELECT id, session_id, restaurant_id, method, amount_minor, currency, status, version, created_at, updated_at
			FROM payments
			WHERE id = $1;
		`, id).Scan(&p.ID, &p.SessionID, &p.RestaurantID, &methStr, &amtMinor, &curr, &statusStr, &p.Version, &p.CreatedAt, &p.UpdatedAt)
		if err == nil {
			p.Method = payment.Method(methStr)
			p.Status = payment.State(statusStr)
			p.Amount = money.New(amtMinor)
			return &p, nil
		}
	}
	return r.mem.GetPaymentByID(ctx, id)
}

func (r *PostgresRepository) GetPaymentsBySessionID(ctx context.Context, sessionID uuid.UUID) ([]payment.Payment, error) {
	if r.pool != nil {
		rows, err := r.pool.Query(ctx, `
			SELECT id, session_id, restaurant_id, method, amount_minor, currency, status, version, created_at, updated_at
			FROM payments
			WHERE session_id = $1;
		`, sessionID)
		if err == nil {
			defer rows.Close()
			var payments []payment.Payment
			for rows.Next() {
				var p payment.Payment
				var methStr, statusStr, curr string
				var amtMinor int64
				if scanErr := rows.Scan(&p.ID, &p.SessionID, &p.RestaurantID, &methStr, &amtMinor, &curr, &statusStr, &p.Version, &p.CreatedAt, &p.UpdatedAt); scanErr == nil {
					p.Method = payment.Method(methStr)
					p.Status = payment.State(statusStr)
					p.Amount = money.New(amtMinor)
					payments = append(payments, p)
				}
			}
			if len(payments) > 0 {
				return payments, nil
			}
		}
	}
	return r.mem.GetPaymentsBySessionID(ctx, sessionID)
}

func (r *PostgresRepository) UpdatePayment(ctx context.Context, p *payment.Payment) error {
	_ = r.mem.UpdatePayment(ctx, p)
	if r.pool != nil {
		_, _ = r.pool.Exec(ctx, `
			UPDATE payments 
			SET status = $1, version = $2, updated_at = $3 
			WHERE id = $4;
		`, p.Status, p.Version, p.UpdatedAt, p.ID)
	}
	return nil
}

func (r *PostgresRepository) CreateRefund(ctx context.Context, ref *payment.Refund) error {
	return r.mem.CreateRefund(ctx, ref)
}

func (r *PostgresRepository) GetRefundsBySessionID(ctx context.Context, sessionID uuid.UUID) ([]payment.Refund, error) {
	return r.mem.GetRefundsBySessionID(ctx, sessionID)
}

func (r *PostgresRepository) CreateAdjustment(ctx context.Context, a *payment.Adjustment) error {
	return r.mem.CreateAdjustment(ctx, a)
}

func (r *PostgresRepository) GetAdjustmentsBySessionID(ctx context.Context, sessionID uuid.UUID) ([]payment.Adjustment, error) {
	return r.mem.GetAdjustmentsBySessionID(ctx, sessionID)
}

// ---------------- ExitPass ----------------

func (r *PostgresRepository) CreateExitPass(ctx context.Context, ep *exitpass.ExitPass) error {
	_ = r.mem.CreateExitPass(ctx, ep)
	if r.pool != nil {
		_, _ = r.pool.Exec(ctx, `
			INSERT INTO exit_passes (id, session_id, restaurant_id, otp_hash, status, expires_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (id) DO NOTHING;
		`, ep.ID, ep.SessionID, ep.RestaurantID, ep.OTPHash, ep.Status, ep.ExpiresAt, ep.CreatedAt, ep.UpdatedAt)
	}
	return nil
}

func (r *PostgresRepository) GetExitPassByID(ctx context.Context, id uuid.UUID) (*exitpass.ExitPass, error) {
	if r.pool != nil {
		var ep exitpass.ExitPass
		var statusStr string
		err := r.pool.QueryRow(ctx, `
			SELECT id, session_id, restaurant_id, otp_hash, status, expires_at, created_at, updated_at
			FROM exit_passes
			WHERE id = $1;
		`, id).Scan(&ep.ID, &ep.SessionID, &ep.RestaurantID, &ep.OTPHash, &statusStr, &ep.ExpiresAt, &ep.CreatedAt, &ep.UpdatedAt)
		if err == nil {
			ep.Status = exitpass.State(statusStr)
			return &ep, nil
		}
	}
	return r.mem.GetExitPassByID(ctx, id)
}

func (r *PostgresRepository) GetExitPassBySessionID(ctx context.Context, sessionID uuid.UUID) (*exitpass.ExitPass, error) {
	if r.pool != nil {
		var ep exitpass.ExitPass
		var statusStr string
		err := r.pool.QueryRow(ctx, `
			SELECT id, session_id, restaurant_id, otp_hash, status, expires_at, created_at, updated_at
			FROM exit_passes
			WHERE session_id = $1
			ORDER BY created_at DESC LIMIT 1;
		`, sessionID).Scan(&ep.ID, &ep.SessionID, &ep.RestaurantID, &ep.OTPHash, &statusStr, &ep.ExpiresAt, &ep.CreatedAt, &ep.UpdatedAt)
		if err == nil {
			ep.Status = exitpass.State(statusStr)
			return &ep, nil
		}
	}
	return r.mem.GetExitPassBySessionID(ctx, sessionID)
}


func (r *PostgresRepository) UpdateExitPass(ctx context.Context, ep *exitpass.ExitPass) error {
	_ = r.mem.UpdateExitPass(ctx, ep)
	if r.pool != nil {
		_, _ = r.pool.Exec(ctx, `
			UPDATE exit_passes 
			SET status = $1, updated_at = $2 
			WHERE id = $3;
		`, ep.Status, ep.UpdatedAt, ep.ID)
	}
	return nil
}

// ---------------- Ledger ----------------

func (r *PostgresRepository) CreatePlatformFeeEntry(ctx context.Context, entry *ledger.PlatformFeeLedgerEntry) error {
	_ = r.mem.CreatePlatformFeeEntry(ctx, entry)
	if r.pool != nil {
		_, _ = r.pool.Exec(ctx, `
			INSERT INTO platform_fee_ledger (id, restaurant_id, session_id, gmv_minor, fee_rate_bps, fee_amount_minor, settlement_status, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (id) DO NOTHING;
		`, entry.ID, entry.RestaurantID, entry.SessionID, entry.GMVAmount.AmountMinorUnits, entry.FeeRateApplied, entry.FeeAmount.AmountMinorUnits, string(entry.SettlementStatus), entry.CreatedAt)
	}
	return nil
}

func (r *PostgresRepository) GetPlatformFeeBySessionID(ctx context.Context, sessionID uuid.UUID) (*ledger.PlatformFeeLedgerEntry, error) {
	return r.mem.GetPlatformFeeBySessionID(ctx, sessionID)
}

func (r *PostgresRepository) ListPlatformFees(ctx context.Context, restaurantID uuid.UUID, period string) ([]ledger.PlatformFeeLedgerEntry, error) {
	return r.mem.ListPlatformFees(ctx, restaurantID, period)
}

func (r *PostgresRepository) CreateRefundAdjustment(ctx context.Context, adj *ledger.RefundAdjustment) error {
	return r.mem.CreateRefundAdjustment(ctx, adj)
}

func (r *PostgresRepository) CreateSettlement(ctx context.Context, s *ledger.RestaurantSettlement) error {
	return r.mem.CreateSettlement(ctx, s)
}

func (r *PostgresRepository) ListSettlements(ctx context.Context, restaurantID uuid.UUID) ([]ledger.RestaurantSettlement, error) {
	return r.mem.ListSettlements(ctx, restaurantID)
}

// ---------------- Restaurant Catalog & Config ----------------

func (r *PostgresRepository) CreateRestaurant(ctx context.Context, rest *restaurant.Restaurant) error {
	_ = r.mem.CreateRestaurant(ctx, rest)
	if r.pool != nil {
		_, _ = r.pool.Exec(ctx, `
			INSERT INTO restaurants (id, name, gstin, commission_rate_bps, settlement_bank_details, status, timezone, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (id) DO NOTHING;
		`, rest.ID, rest.Name, rest.GSTIN, rest.CommissionRateBps, rest.SettlementBankDetails, rest.Status, rest.Timezone, rest.CreatedAt, rest.UpdatedAt)
	}
	return nil
}

func (r *PostgresRepository) GetRestaurantByID(ctx context.Context, id uuid.UUID) (*restaurant.Restaurant, error) {
	if r.pool != nil {
		var rest restaurant.Restaurant
		err := r.pool.QueryRow(ctx, `
			SELECT id, name, gstin, commission_rate_bps, settlement_bank_details, status, timezone, created_at, updated_at
			FROM restaurants
			WHERE id = $1;
		`, id).Scan(&rest.ID, &rest.Name, &rest.GSTIN, &rest.CommissionRateBps, &rest.SettlementBankDetails, &rest.Status, &rest.Timezone, &rest.CreatedAt, &rest.UpdatedAt)
		if err == nil {
			return &rest, nil
		}
	}
	return r.mem.GetRestaurantByID(ctx, id)
}

func (r *PostgresRepository) ListRestaurants(ctx context.Context) ([]restaurant.Restaurant, error) {
	return r.mem.ListRestaurants(ctx)
}

func (r *PostgresRepository) UpdateRestaurant(ctx context.Context, rest *restaurant.Restaurant) error {
	return r.mem.UpdateRestaurant(ctx, rest)
}

func (r *PostgresRepository) CreateTable(ctx context.Context, t *restaurant.Table) error {
	_ = r.mem.CreateTable(ctx, t)
	if r.pool != nil {
		_, _ = r.pool.Exec(ctx, `
			INSERT INTO tables (id, restaurant_id, table_number, table_token, is_active, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (id) DO NOTHING;
		`, t.ID, t.RestaurantID, t.TableNumber, t.TableToken, t.IsActive, t.CreatedAt, t.UpdatedAt)
	}
	return nil
}

func (r *PostgresRepository) GetTableByID(ctx context.Context, id uuid.UUID) (*restaurant.Table, error) {
	if r.pool != nil {
		var t restaurant.Table
		err := r.pool.QueryRow(ctx, `
			SELECT id, restaurant_id, table_number, table_token, is_active, created_at, updated_at
			FROM tables
			WHERE id = $1;
		`, id).Scan(&t.ID, &t.RestaurantID, &t.TableNumber, &t.TableToken, &t.IsActive, &t.CreatedAt, &t.UpdatedAt)
		if err == nil {
			return &t, nil
		}
	}
	return r.mem.GetTableByID(ctx, id)
}

func (r *PostgresRepository) GetTableByToken(ctx context.Context, token string) (*restaurant.Table, error) {
	if r.pool != nil {
		var t restaurant.Table
		err := r.pool.QueryRow(ctx, `
			SELECT id, restaurant_id, table_number, table_token, is_active, created_at, updated_at
			FROM tables
			WHERE table_token = $1 AND is_active = true;
		`, token).Scan(&t.ID, &t.RestaurantID, &t.TableNumber, &t.TableToken, &t.IsActive, &t.CreatedAt, &t.UpdatedAt)
		if err == nil {
			return &t, nil
		}
	}
	return r.mem.GetTableByToken(ctx, token)
}

func (r *PostgresRepository) ListTables(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.Table, error) {
	return r.mem.ListTables(ctx, restaurantID)
}

func (r *PostgresRepository) CreateStaff(ctx context.Context, s *restaurant.StaffUser) error {
	_ = r.mem.CreateStaff(ctx, s)
	if r.pool != nil {
		_, err := r.pool.Exec(ctx, `
			INSERT INTO staff_users (id, restaurant_id, employee_id, name, phone, email, password_hash, role, is_active, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (id) DO UPDATE SET employee_id = EXCLUDED.employee_id, is_active = EXCLUDED.is_active, updated_at = EXCLUDED.updated_at;
		`, s.ID, s.RestaurantID, s.EmployeeID, s.Name, s.Phone, s.Email, s.PasswordHash, string(s.Role), s.IsActive, s.CreatedAt, s.UpdatedAt)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *PostgresRepository) GetStaffByID(ctx context.Context, id uuid.UUID) (*restaurant.StaffUser, error) {
	if r.pool != nil {
		var s restaurant.StaffUser
		var roleStr string
		err := r.pool.QueryRow(ctx, `
			SELECT id, restaurant_id, COALESCE(employee_id, ''), name, phone, email, password_hash, role, is_active, created_at, updated_at
			FROM staff_users
			WHERE id = $1;
		`, id).Scan(&s.ID, &s.RestaurantID, &s.EmployeeID, &s.Name, &s.Phone, &s.Email, &s.PasswordHash, &roleStr, &s.IsActive, &s.CreatedAt, &s.UpdatedAt)
		if err == nil {
			s.Role = restaurant.Role(roleStr)
			return &s, nil
		}
	}
	return r.mem.GetStaffByID(ctx, id)
}

func (r *PostgresRepository) GetStaffByEmail(ctx context.Context, email string) (*restaurant.StaffUser, error) {
	if r.pool != nil {
		var s restaurant.StaffUser
		var roleStr string
		err := r.pool.QueryRow(ctx, `
			SELECT id, restaurant_id, COALESCE(employee_id, ''), name, phone, email, password_hash, role, is_active, created_at, updated_at
			FROM staff_users
			WHERE email = $1;
		`, email).Scan(&s.ID, &s.RestaurantID, &s.EmployeeID, &s.Name, &s.Phone, &s.Email, &s.PasswordHash, &roleStr, &s.IsActive, &s.CreatedAt, &s.UpdatedAt)
		if err == nil {
			s.Role = restaurant.Role(roleStr)
			return &s, nil
		}
	}
	return r.mem.GetStaffByEmail(ctx, email)
}

func (r *PostgresRepository) GetStaffByEmployeeID(ctx context.Context, restaurantID uuid.UUID, employeeID string) (*restaurant.StaffUser, error) {
	if r.pool != nil {
		var s restaurant.StaffUser
		var roleStr string
		err := r.pool.QueryRow(ctx, `
			SELECT id, restaurant_id, COALESCE(employee_id, ''), name, phone, email, password_hash, role, is_active, created_at, updated_at
			FROM staff_users
			WHERE restaurant_id = $1 AND employee_id = $2;
		`, restaurantID, employeeID).Scan(&s.ID, &s.RestaurantID, &s.EmployeeID, &s.Name, &s.Phone, &s.Email, &s.PasswordHash, &roleStr, &s.IsActive, &s.CreatedAt, &s.UpdatedAt)
		if err == nil {
			s.Role = restaurant.Role(roleStr)
			return &s, nil
		}
	}
	return r.mem.GetStaffByEmployeeID(ctx, restaurantID, employeeID)
}

func (r *PostgresRepository) ListStaff(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.StaffUser, error) {
	if r.pool != nil {
		rows, err := r.pool.Query(ctx, `
			SELECT id, restaurant_id, COALESCE(employee_id, ''), name, phone, email, password_hash, role, is_active, created_at, updated_at
			FROM staff_users
			WHERE restaurant_id = $1
			ORDER BY created_at ASC;
		`, restaurantID)
		if err == nil {
			defer rows.Close()
			var staffList []restaurant.StaffUser
			for rows.Next() {
				var s restaurant.StaffUser
				var roleStr string
				if err := rows.Scan(&s.ID, &s.RestaurantID, &s.EmployeeID, &s.Name, &s.Phone, &s.Email, &s.PasswordHash, &roleStr, &s.IsActive, &s.CreatedAt, &s.UpdatedAt); err == nil {
					s.Role = restaurant.Role(roleStr)
					staffList = append(staffList, s)
				}
			}
			return staffList, nil
		}
	}
	return r.mem.ListStaff(ctx, restaurantID)
}

func (r *PostgresRepository) CreateGuard(ctx context.Context, g *restaurant.GuardUser) error {
	_ = r.mem.CreateGuard(ctx, g)
	if r.pool != nil {
		_, err := r.pool.Exec(ctx, `
			INSERT INTO guard_users (id, restaurant_id, name, phone, password_hash, is_active, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (id) DO UPDATE SET is_active = EXCLUDED.is_active, updated_at = EXCLUDED.updated_at;
		`, g.ID, g.RestaurantID, g.Name, g.Phone, g.PasswordHash, g.IsActive, g.CreatedAt, g.UpdatedAt)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *PostgresRepository) GetGuardByID(ctx context.Context, id uuid.UUID) (*restaurant.GuardUser, error) {
	if r.pool != nil {
		var g restaurant.GuardUser
		err := r.pool.QueryRow(ctx, `
			SELECT id, restaurant_id, name, phone, password_hash, is_active, created_at, updated_at
			FROM guard_users
			WHERE id = $1;
		`, id).Scan(&g.ID, &g.RestaurantID, &g.Name, &g.Phone, &g.PasswordHash, &g.IsActive, &g.CreatedAt, &g.UpdatedAt)
		if err == nil {
			return &g, nil
		}
	}
	return r.mem.GetGuardByID(ctx, id)
}

func (r *PostgresRepository) GetGuardByPhone(ctx context.Context, phone string) (*restaurant.GuardUser, error) {
	if r.pool != nil {
		var g restaurant.GuardUser
		err := r.pool.QueryRow(ctx, `
			SELECT id, restaurant_id, name, phone, password_hash, is_active, created_at, updated_at
			FROM guard_users
			WHERE phone = $1;
		`, phone).Scan(&g.ID, &g.RestaurantID, &g.Name, &g.Phone, &g.PasswordHash, &g.IsActive, &g.CreatedAt, &g.UpdatedAt)
		if err == nil {
			return &g, nil
		}
	}
	return r.mem.GetGuardByPhone(ctx, phone)
}


func (r *PostgresRepository) CreateCategory(ctx context.Context, c *restaurant.MenuCategory) error {
	_ = r.mem.CreateCategory(ctx, c)
	if r.pool != nil {
		_, err := r.pool.Exec(ctx, `
			INSERT INTO menu_categories (id, restaurant_id, name, display_order, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, display_order = EXCLUDED.display_order, updated_at = EXCLUDED.updated_at;
		`, c.ID, c.RestaurantID, c.Name, c.DisplayOrder, c.CreatedAt, c.UpdatedAt)
		return err
	}
	return nil
}

func (r *PostgresRepository) ListCategories(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.MenuCategory, error) {
	if r.pool != nil {
		rows, err := r.pool.Query(ctx, `
			SELECT id, restaurant_id, name, display_order, created_at, updated_at 
			FROM menu_categories 
			WHERE restaurant_id = $1 
			ORDER BY display_order ASC;
		`, restaurantID)
		if err == nil {
			defer rows.Close()
			var cats []restaurant.MenuCategory
			for rows.Next() {
				var c restaurant.MenuCategory
				if err := rows.Scan(&c.ID, &c.RestaurantID, &c.Name, &c.DisplayOrder, &c.CreatedAt, &c.UpdatedAt); err == nil {
					cats = append(cats, c)
				}
			}
			if len(cats) > 0 {
				return cats, nil
			}
		}
	}
	return r.mem.ListCategories(ctx, restaurantID)
}

func (r *PostgresRepository) CreateMenuItem(ctx context.Context, m *restaurant.MenuItem) error {
	_ = r.mem.CreateMenuItem(ctx, m)
	if r.pool != nil {
		_, err := r.pool.Exec(ctx, `
			INSERT INTO menu_items (id, restaurant_id, category_id, name, description, price_minor, currency, is_available, hsn_sac_code, cgst_rate_bps, sgst_rate_bps, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description, price_minor = EXCLUDED.price_minor, is_available = EXCLUDED.is_available, updated_at = EXCLUDED.updated_at;
		`, m.ID, m.RestaurantID, m.CategoryID, m.Name, m.Description, m.Price.AmountMinorUnits, m.Price.Currency, m.IsAvailable, m.HSNSACCode, m.CGSTRateBps, m.SGSTRateBps, m.CreatedAt, m.UpdatedAt)
		return err
	}
	return nil
}

func (r *PostgresRepository) GetMenuItemByID(ctx context.Context, id uuid.UUID) (*restaurant.MenuItem, error) {
	if r.pool != nil {
		var it restaurant.MenuItem
		var priceMinor int64
		var curr string
		err := r.pool.QueryRow(ctx, `
			SELECT id, restaurant_id, category_id, name, description, price_minor, currency, is_available, hsn_sac_code, cgst_rate_bps, sgst_rate_bps, created_at, updated_at
			FROM menu_items
			WHERE id = $1;
		`, id).Scan(&it.ID, &it.RestaurantID, &it.CategoryID, &it.Name, &it.Description, &priceMinor, &curr, &it.IsAvailable, &it.HSNSACCode, &it.CGSTRateBps, &it.SGSTRateBps, &it.CreatedAt, &it.UpdatedAt)
		if err == nil {
			it.Price = money.New(priceMinor)
			return &it, nil
		}
	}
	return r.mem.GetMenuItemByID(ctx, id)
}

func (r *PostgresRepository) ListMenuItems(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.MenuItem, error) {
	if r.pool != nil {
		rows, err := r.pool.Query(ctx, `
			SELECT id, restaurant_id, category_id, name, description, price_minor, currency, is_available, hsn_sac_code, cgst_rate_bps, sgst_rate_bps, created_at, updated_at
			FROM menu_items
			WHERE restaurant_id = $1;
		`, restaurantID)
		if err == nil {
			defer rows.Close()
			var items []restaurant.MenuItem
			for rows.Next() {
				var it restaurant.MenuItem
				var priceMinor int64
				var curr string
				if err := rows.Scan(&it.ID, &it.RestaurantID, &it.CategoryID, &it.Name, &it.Description, &priceMinor, &curr, &it.IsAvailable, &it.HSNSACCode, &it.CGSTRateBps, &it.SGSTRateBps, &it.CreatedAt, &it.UpdatedAt); err == nil {
					it.Price = money.New(priceMinor)
					items = append(items, it)
				}
			}
			if len(items) > 0 {
				return items, nil
			}
		}
	}
	return r.mem.ListMenuItems(ctx, restaurantID)
}

func (r *PostgresRepository) UpdateMenuItemAvailability(ctx context.Context, id uuid.UUID, isAvailable bool) error {
	_ = r.mem.UpdateMenuItemAvailability(ctx, id, isAvailable)
	if r.pool != nil {
		_, _ = r.pool.Exec(ctx, `UPDATE menu_items SET is_available = $1, updated_at = $2 WHERE id = $3`, isAvailable, time.Now().UTC(), id)
	}
	return nil
}

func (r *PostgresRepository) GetSettings(ctx context.Context, restaurantID uuid.UUID) (*restaurant.RestaurantSettings, error) {
	return r.mem.GetSettings(ctx, restaurantID)
}

func (r *PostgresRepository) UpdateSettings(ctx context.Context, s *restaurant.RestaurantSettings) error {
	return r.mem.UpdateSettings(ctx, s)
}

func (r *PostgresRepository) GetOnboarding(ctx context.Context, restaurantID uuid.UUID) (*restaurant.RestaurantOnboarding, error) {
	return r.mem.GetOnboarding(ctx, restaurantID)
}

func (r *PostgresRepository) UpdateOnboarding(ctx context.Context, o *restaurant.RestaurantOnboarding) error {
	return r.mem.UpdateOnboarding(ctx, o)
}

// ---------------- Audit & Outbox ----------------

func (r *PostgresRepository) AppendAuditLog(ctx context.Context, log *audit.AuditLog) error {
	_ = r.mem.AppendAuditLog(ctx, log)
	if r.pool != nil {
		_, _ = r.pool.Exec(ctx, `
			INSERT INTO audit_logs (id, restaurant_id, session_id, actor_type, actor_id, action, before_state, after_state, metadata, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (id) DO NOTHING;
		`, log.ID, log.RestaurantID, log.SessionID, string(log.ActorType), log.ActorID, log.Action, log.BeforeState, log.AfterState, log.Metadata, log.CreatedAt)
	}
	return nil
}

func (r *PostgresRepository) AppendStaffAction(ctx context.Context, action *audit.StaffAction) error {
	_ = r.mem.AppendStaffAction(ctx, action)
	if r.pool != nil {
		var staffExists bool
		if action.StaffID != uuid.Nil {
			_ = r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM staff_users WHERE id = $1)`, action.StaffID).Scan(&staffExists)
		}
		if staffExists && action.AuditLogID != uuid.Nil {
			_, _ = r.pool.Exec(ctx, `
				INSERT INTO staff_actions (id, audit_log_id, staff_id, restaurant_id, session_id, action_type, reason, metadata, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
				ON CONFLICT (id) DO NOTHING;
			`, action.ID, action.AuditLogID, action.StaffID, action.RestaurantID, action.SessionID, action.ActionType, action.Reason, action.Metadata, action.CreatedAt)
		}
	}
	return nil
}

func (r *PostgresRepository) ListAuditLogs(ctx context.Context, restaurantID uuid.UUID, limit, offset int) ([]audit.AuditLog, error) {
	return r.mem.ListAuditLogs(ctx, restaurantID, limit, offset)
}

func (r *PostgresRepository) ListStaffActions(ctx context.Context, restaurantID uuid.UUID, limit, offset int) ([]audit.StaffAction, error) {
	return r.mem.ListStaffActions(ctx, restaurantID, limit, offset)
}

func (r *PostgresRepository) StoreOutboxEvent(ctx context.Context, event *storage.OutboxEvent) error {
	return r.mem.StoreOutboxEvent(ctx, event)
}

func (r *PostgresRepository) FetchPendingOutbox(ctx context.Context, batchSize int) ([]storage.OutboxEvent, error) {
	return r.mem.FetchPendingOutbox(ctx, batchSize)
}

func (r *PostgresRepository) ClaimPendingOutbox(ctx context.Context, batchSize int, leaseDuration time.Duration) ([]storage.OutboxEvent, error) {
	return r.mem.ClaimPendingOutbox(ctx, batchSize, leaseDuration)
}

func (r *PostgresRepository) MarkOutboxPublished(ctx context.Context, id uuid.UUID) error {
	return r.mem.MarkOutboxPublished(ctx, id)
}

func (r *PostgresRepository) MarkOutboxFailed(ctx context.Context, id uuid.UUID, lastErr string, backoff time.Duration, maxRetries int) error {
	return r.mem.MarkOutboxFailed(ctx, id, lastErr, backoff, maxRetries)
}

func (r *PostgresRepository) ReplayDeadLetterOutbox(ctx context.Context, id uuid.UUID) error {
	return r.mem.ReplayDeadLetterOutbox(ctx, id)
}

// ---------------- Webhook Idempotency ----------------

func (r *PostgresRepository) RecordWebhookEvent(ctx context.Context, gateway, eventID string) (bool, error) {
	if r.pool != nil {
		tag, err := r.pool.Exec(ctx, `
			INSERT INTO webhook_events (id, gateway, event_id, processed_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (gateway, event_id) DO NOTHING;
		`, uuid.New(), gateway, eventID, time.Now().UTC())
		if err == nil {
			return tag.RowsAffected() > 0, nil
		}
	}
	return r.mem.RecordWebhookEvent(ctx, gateway, eventID)
}

// Ensure interface compliance
var _ storage.Repository = (*PostgresRepository)(nil)
