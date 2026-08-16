package promotion

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Repository interface {
	Create(ctx context.Context, db bun.IDB, p *Promotion) error
	FindByID(ctx context.Context, id uuid.UUID) (*Promotion, error)
	FindByIDForUpdate(ctx context.Context, tx bun.IDB, id uuid.UUID) (*Promotion, error)
	FindByCodeForUpdate(ctx context.Context, tx bun.IDB, code string) (*Promotion, error)
	FindByCodeInsensitive(ctx context.Context, code string) (*Promotion, error)
	FindActiveByMerchant(ctx context.Context, merchantID *uuid.UUID) ([]Promotion, error)
	FindAutoActiveForMerchantForUpdate(ctx context.Context, tx bun.IDB, merchantID *uuid.UUID) (*Promotion, error)
	List(ctx context.Context, merchantID *uuid.UUID, isActive *bool, search string, firstPurchaseOnly *bool, offset, limit int) ([]Promotion, int, error)
	Update(ctx context.Context, db bun.IDB, p *Promotion) error
	Delete(ctx context.Context, db bun.IDB, id uuid.UUID) error

	InsertUsage(ctx context.Context, tx bun.IDB, u *PromotionUsage) error
	FindUsageByOrderID(ctx context.Context, orderID uuid.UUID) (*PromotionUsage, error)
	DeleteUsageByOrderID(ctx context.Context, tx bun.IDB, orderID uuid.UUID) (*PromotionUsage, error)
	ListUsages(ctx context.Context, promotionID uuid.UUID, offset, limit int) ([]PromotionUsage, int, error)
	CountUserUsage(ctx context.Context, promotionID, userID uuid.UUID) (int, error)
	CountUserCompletedOrders(ctx context.Context, userID uuid.UUID) (int, error)
	GetAvgOrderValue(ctx context.Context, merchantID *uuid.UUID) (float64, error)
	GetSettlement(ctx context.Context, merchantID *uuid.UUID, from, to *time.Time) ([]SettlementItem, float64, int, error)
	UpdateMerchantNameBatch(ctx context.Context, promotions []Promotion) ([]Promotion, error)
}

type repository struct {
	db *bun.DB
}

func NewRepository(db *bun.DB) Repository {
	return &repository{db: db}
}

func (r *repository) Create(ctx context.Context, db bun.IDB, p *Promotion) error {
	_, err := db.NewInsert().Model(p).Exec(ctx)
	return err
}

func (r *repository) FindByID(ctx context.Context, id uuid.UUID) (*Promotion, error) {
	p := &Promotion{}
	err := r.db.NewSelect().Model(p).Where("p.id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (r *repository) FindByIDForUpdate(ctx context.Context, tx bun.IDB, id uuid.UUID) (*Promotion, error) {
	p := &Promotion{}
	err := tx.NewSelect().Model(p).Where("p.id = ?", id).For("UPDATE").Scan(ctx)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (r *repository) FindByCodeForUpdate(ctx context.Context, tx bun.IDB, code string) (*Promotion, error) {
	p := &Promotion{}
	err := tx.NewSelect().Model(p).Where("LOWER(p.code) = LOWER(?)", code).For("UPDATE").Scan(ctx)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (r *repository) FindByCodeInsensitive(ctx context.Context, code string) (*Promotion, error) {
	p := &Promotion{}
	err := r.db.NewSelect().Model(p).Where("LOWER(p.code) = LOWER(?)", code).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (r *repository) FindActiveByMerchant(ctx context.Context, merchantID *uuid.UUID) ([]Promotion, error) {
	var list []Promotion
	q := r.db.NewSelect().Model(&list).Where("p.is_active = true").Where("p.used_count < p.max_uses").Where("p.budget_used < p.budget_total")
	q = q.WhereGroup(" AND ", func(sq *bun.SelectQuery) *bun.SelectQuery {
		sq = sq.Where("p.valid_from IS NULL OR p.valid_from <= NOW()")
		sq = sq.Where("p.valid_until IS NULL OR p.valid_until >= NOW()")
		return sq
	})
	if merchantID != nil {
		q = q.WhereGroup(" AND ", func(sq *bun.SelectQuery) *bun.SelectQuery {
			sq = sq.Where("p.merchant_id = ? OR p.merchant_id IS NULL", *merchantID)
			return sq
		})
	}
	q = q.Order("p.auto_apply DESC, p.created_at ASC")
	err := q.Scan(ctx)
	return list, err
}

func (r *repository) FindAutoActiveForMerchantForUpdate(ctx context.Context, tx bun.IDB, merchantID *uuid.UUID) (*Promotion, error) {
	p := &Promotion{}
	q := tx.NewSelect().Model(p).Where("p.is_active = true").Where("p.auto_apply = true")
	q = q.Where("p.used_count < p.max_uses").Where("p.budget_used < p.budget_total")
	q = q.Where("p.valid_from IS NULL OR p.valid_from <= NOW()")
	q = q.Where("p.valid_until IS NULL OR p.valid_until >= NOW()")
	if merchantID != nil {
		q = q.Where("p.merchant_id = ? OR p.merchant_id IS NULL", *merchantID)
	}
	q = q.Order("p.created_at ASC").Limit(1).For("UPDATE")
	err := q.Scan(ctx)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (r *repository) List(ctx context.Context, merchantID *uuid.UUID, isActive *bool, search string, firstPurchaseOnly *bool, offset, limit int) ([]Promotion, int, error) {
	var list []Promotion
	q := r.db.NewSelect().Model(&list)
	countQ := r.db.NewSelect().Model((*Promotion)(nil))

	if merchantID != nil {
		q = q.Where("p.merchant_id = ?", *merchantID)
		countQ = countQ.Where("merchant_id = ?", *merchantID)
	}
	if isActive != nil {
		q = q.Where("p.is_active = ?", *isActive)
		countQ = countQ.Where("is_active = ?", *isActive)
	}
	if firstPurchaseOnly != nil {
		q = q.Where("p.first_purchase_only = ?", *firstPurchaseOnly)
		countQ = countQ.Where("first_purchase_only = ?", *firstPurchaseOnly)
	}
	if search != "" {
		like := "%" + search + "%"
		q = q.WhereGroup(" AND ", func(sq *bun.SelectQuery) *bun.SelectQuery {
			sq = sq.Where("p.title ILIKE ? OR p.code ILIKE ? OR p.description ILIKE ?", like, like, like)
			return sq
		})
		countQ = countQ.WhereGroup(" AND ", func(sq *bun.SelectQuery) *bun.SelectQuery {
			sq = sq.Where("title ILIKE ? OR code ILIKE ? OR description ILIKE ?", like, like, like)
			return sq
		})
	}

	total, err := countQ.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	q = q.Order("p.created_at DESC").Offset(offset).Limit(limit)
	err = q.Scan(ctx)
	return list, total, err
}

func (r *repository) Update(ctx context.Context, db bun.IDB, p *Promotion) error {
	p.UpdatedAt = time.Now()
	_, err := db.NewUpdate().Model(p).WherePK().Exec(ctx)
	return err
}

func (r *repository) Delete(ctx context.Context, db bun.IDB, id uuid.UUID) error {
	_, err := db.NewDelete().Model((*Promotion)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}

func (r *repository) InsertUsage(ctx context.Context, tx bun.IDB, u *PromotionUsage) error {
	_, err := tx.NewInsert().Model(u).Exec(ctx)
	return err
}

func (r *repository) FindUsageByOrderID(ctx context.Context, orderID uuid.UUID) (*PromotionUsage, error) {
	u := &PromotionUsage{}
	err := r.db.NewSelect().Model(u).Where("pu.order_id = ?", orderID).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (r *repository) DeleteUsageByOrderID(ctx context.Context, tx bun.IDB, orderID uuid.UUID) (*PromotionUsage, error) {
	u := &PromotionUsage{}
	err := tx.NewSelect().Model(u).Where("pu.order_id = ?", orderID).For("UPDATE").Scan(ctx)
	if err != nil {
		return nil, err
	}
	_, err = tx.NewDelete().Model((*PromotionUsage)(nil)).Where("order_id = ?", orderID).Exec(ctx)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (r *repository) ListUsages(ctx context.Context, promotionID uuid.UUID, offset, limit int) ([]PromotionUsage, int, error) {
	var list []PromotionUsage
	q := r.db.NewSelect().Model(&list).Where("pu.promotion_id = ?", promotionID).Order("pu.created_at DESC").Offset(offset).Limit(limit)
	total, err := r.db.NewSelect().Model((*PromotionUsage)(nil)).Where("promotion_id = ?", promotionID).Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	err = q.Scan(ctx)
	return list, total, err
}

func (r *repository) CountUserUsage(ctx context.Context, promotionID, userID uuid.UUID) (int, error) {
	return r.db.NewSelect().Model((*PromotionUsage)(nil)).Where("promotion_id = ? AND user_id = ?", promotionID, userID).Count(ctx)
}

func (r *repository) CountUserCompletedOrders(ctx context.Context, userID uuid.UUID) (int, error) {
	cnt, err := r.db.NewSelect().Model((*struct {
		bun.BaseModel `bun:"table:orders,alias:o"`
	})(nil)).Where("o.requester_id = ? AND o.status = 'completed'", userID).Count(ctx)
	return cnt, err
}

func (r *repository) GetAvgOrderValue(ctx context.Context, merchantID *uuid.UUID) (float64, error) {
	// Try merchant_surveys first
	if merchantID != nil {
		var survey struct {
			Avg float64 `bun:"avg"`
		}
		err := r.db.NewSelect().Table("merchant_surveys").ColumnExpr("average_item_price as avg").Where("merchant_id = ?", *merchantID).Order("created_at DESC").Limit(1).Scan(ctx, &survey)
		if err == nil && survey.Avg > 0 {
			// average_item_price * assumed 1.8 items avg for order total preview
			avgVal := survey.Avg * 1.8
			if avgVal > 0 {
				return avgVal, nil
			}
		}

		// Then average of completed orders last 30 days
		var orderAvg struct {
			Avg *float64 `bun:"avg"`
		}
		err = r.db.NewSelect().Table("orders").ColumnExpr("AVG(estimated_cost) as avg").Where("merchant_id = ? AND status = 'completed' AND created_at > NOW() - INTERVAL '30 days'", *merchantID).Scan(ctx, &orderAvg)
		if err == nil && orderAvg.Avg != nil && *orderAvg.Avg > 0 {
			return *orderAvg.Avg, nil
		}
	}

	// fallback default avg order value
	return 25000.0, nil
}

func (r *repository) GetSettlement(ctx context.Context, merchantID *uuid.UUID, from, to *time.Time) ([]SettlementItem, float64, int, error) {
	// Aggregate from promotion_usages joined with orders where status completed
	// SELECT merchant_id, SUM(discount_amount) total, COUNT(*) cnt FROM promotion_usages pu JOIN orders o ON o.id=pu.order_id WHERE o.status='completed' ...
	type row struct {
		MerchantID     *uuid.UUID `bun:"merchant_id"`
		TotalLiability float64    `bun:"total_liability"`
		OrderCount     int        `bun:"order_count"`
	}
	var rows []row
	q := r.db.NewSelect().TableExpr("promotion_usages AS pu").
		ColumnExpr("pu.merchant_id as merchant_id").
		ColumnExpr("SUM(pu.discount_amount) as total_liability").
		ColumnExpr("COUNT(*) as order_count").
		Join("JOIN orders o ON o.id = pu.order_id").
		Where("o.status = 'completed'")

	if merchantID != nil {
		q = q.Where("pu.merchant_id = ?", *merchantID)
	}
	if from != nil {
		q = q.Where("pu.created_at >= ?", *from)
	}
	if to != nil {
		q = q.Where("pu.created_at <= ?", *to)
	}
	q = q.Group("pu.merchant_id")
	err := q.Scan(ctx, &rows)
	if err != nil {
		return nil, 0, 0, err
	}

	var totalLiab float64
	var totalCnt int
	items := make([]SettlementItem, 0, len(rows))
	for _, rr := range rows {
		totalLiab += rr.TotalLiability
		totalCnt += rr.OrderCount
		items = append(items, SettlementItem{
			MerchantID:     rr.MerchantID,
			TotalLiability: rr.TotalLiability,
			OrderCount:     rr.OrderCount,
		})
	}

	// Enrich merchant names if needed
	if len(items) > 0 {
		// batch fetch merchant names
		var mids []uuid.UUID
		for _, it := range items {
			if it.MerchantID != nil {
				mids = append(mids, *it.MerchantID)
			}
		}
		if len(mids) > 0 {
			type mRow struct {
				ID   uuid.UUID `bun:"id"`
				Name string    `bun:"name"`
			}
			var mRows []mRow
			err = r.db.NewSelect().Table("merchants").Column("id", "name").Where("id IN (?)", bun.List(mids)).Scan(ctx, &mRows)
			if err == nil {
				mMap := make(map[uuid.UUID]string)
				for _, mr := range mRows {
					mMap[mr.ID] = mr.Name
				}
				for i := range items {
					if items[i].MerchantID != nil {
						if n, ok := mMap[*items[i].MerchantID]; ok {
							items[i].MerchantName = n
						}
					}
				}
			}
		}
	}

	return items, totalLiab, totalCnt, nil
}

func (r *repository) UpdateMerchantNameBatch(ctx context.Context, promotions []Promotion) ([]Promotion, error) {
	if len(promotions) == 0 {
		return promotions, nil
	}
	var mids []uuid.UUID
	for _, p := range promotions {
		if p.MerchantID != nil {
			mids = append(mids, *p.MerchantID)
		}
	}
	if len(mids) == 0 {
		return promotions, nil
	}
	type mRow struct {
		ID   uuid.UUID `bun:"id"`
		Name string    `bun:"name"`
	}
	var mRows []mRow
	err := r.db.NewSelect().Table("merchants").Column("id", "name").Where("id IN (?)", bun.List(mids)).Scan(ctx, &mRows)
	if err != nil {
		return promotions, nil
	}
	mMap := make(map[uuid.UUID]string)
	for _, mr := range mRows {
		mMap[mr.ID] = mr.Name
	}
	for i := range promotions {
		if promotions[i].MerchantID != nil {
			if n, ok := mMap[*promotions[i].MerchantID]; ok {
				promotions[i].MerchantName = n
			}
		}
	}
	return promotions, nil
}
