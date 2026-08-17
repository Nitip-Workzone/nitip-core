package support

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Repository interface {
	CreateTicket(ctx context.Context, ticket *Ticket) error
	FindTicketByID(ctx context.Context, id uuid.UUID) (*Ticket, error)
	FindTicketByIDForUpdate(ctx context.Context, id uuid.UUID) (*Ticket, error)
	FindTicketsByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Ticket, error)
	FindAllTickets(ctx context.Context, status, category, search string, assignedCSID *uuid.UUID, limit, offset int) ([]Ticket, int, error)
	FindQueueTickets(ctx context.Context, limit, offset int) ([]Ticket, int, error)
	UpdateTicket(ctx context.Context, ticket *Ticket) error
	ClaimTicketAtomic(ctx context.Context, ticketID, csID uuid.UUID) (*Ticket, error)
	CountActiveByCS(ctx context.Context, csID uuid.UUID) (int, error)
	AutoCloseResolved(ctx context.Context, days int) (int, error)
	GetStats(ctx context.Context) (map[string]int, error)

	CreateMessage(ctx context.Context, msg *Message) error
	FindMessagesByTicketID(ctx context.Context, ticketID uuid.UUID, afterID *uuid.UUID, afterTime *string, limit int, includeInternal bool) ([]Message, error)

	CreateFAQ(ctx context.Context, faq *FAQ) error
	UpdateFAQ(ctx context.Context, faq *FAQ) error
	DeleteFAQ(ctx context.Context, id uuid.UUID) error
	FindFAQByID(ctx context.Context, id uuid.UUID) (*FAQ, error)
	FindAllFAQ(ctx context.Context, activeOnly bool) ([]FAQ, error)
	SearchFAQ(ctx context.Context, query, category string, limit int) ([]FAQ, error)
}

type repository struct {
	db *bun.DB
}

func NewRepository(db *bun.DB) Repository {
	return &repository{db: db}
}

func (r *repository) CreateTicket(ctx context.Context, ticket *Ticket) error {
	_, err := r.db.NewInsert().Model(ticket).Exec(ctx)
	return err
}

func (r *repository) FindTicketByID(ctx context.Context, id uuid.UUID) (*Ticket, error) {
	t := new(Ticket)
	err := r.db.NewSelect().
		Model(t).
		Column("st.*").
		ColumnExpr("u.name AS user_name").
		ColumnExpr("u.email AS user_email").
		ColumnExpr("u.whatsapp_number AS user_whatsapp").
		ColumnExpr("cs.name AS assigned_cs_name").
		ColumnExpr("cs.whatsapp_number AS assigned_cs_whatsapp").
		Join("LEFT JOIN users AS u ON u.id = st.user_id").
		Join("LEFT JOIN users AS cs ON cs.id = st.assigned_cs_id").
		Where("st.id = ?", id).
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (r *repository) FindTicketByIDForUpdate(ctx context.Context, id uuid.UUID) (*Ticket, error) {
	t := new(Ticket)
	// FOR UPDATE to prevent concurrent claim race (prod 512M + 200 max_conn)
	err := r.db.NewSelect().Model(t).Where("id = ?", id).For("UPDATE").Scan(ctx)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (r *repository) ClaimTicketAtomic(ctx context.Context, ticketID, csID uuid.UUID) (*Ticket, error) {
	// Atomic conditional update: only claim if still queued/open and unassigned
	// Returns updated ticket or error, prevents double claim without separate SELECT
	var t Ticket
	err := r.db.NewSelect().Model(&t).Where("id = ?", ticketID).For("UPDATE").Scan(ctx)
	if err != nil {
		return nil, err
	}
	if t.Status != StatusQueued && t.Status != StatusOpen {
		return nil, fmt.Errorf("tiket tidak dalam antrian")
	}
	if t.AssignedCSID != nil {
		return nil, fmt.Errorf("tiket sudah diambil CS lain")
	}
	// Perform atomic update with WHERE status still in queue to double guard
	res, err := r.db.NewUpdate().Model((*Ticket)(nil)).
		Set("assigned_cs_id = ?", csID).
		Set("status = ?", StatusInProgress).
		Set("updated_at = NOW()").
		Where("id = ?", ticketID).
		Where("status IN ('queued','open')").
		Where("assigned_cs_id IS NULL").
		Exec(ctx)
	if err != nil {
		return nil, err
	}
	aff, _ := res.RowsAffected()
	if aff == 0 {
		return nil, fmt.Errorf("tiket sudah diambil CS lain (race lost)")
	}
	// Re-fetch final
	err = r.db.NewSelect().Model(&t).Where("id = ?", ticketID).Scan(ctx)
	return &t, err
}

func (r *repository) FindTicketsByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Ticket, error) {
	var tickets []Ticket
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	q := r.db.NewSelect().
		Model(&tickets).
		Column("st.*").
		ColumnExpr("cs.name AS assigned_cs_name").
		ColumnExpr("cs.whatsapp_number AS assigned_cs_whatsapp").
		Join("LEFT JOIN users AS cs ON cs.id = st.assigned_cs_id").
		Where("st.user_id = ?", userID).
		Order("st.created_at DESC").
		Limit(limit).
		Offset(offset)
	err := q.Scan(ctx)
	return tickets, err
}

func (r *repository) FindAllTickets(ctx context.Context, status, category, search string, assignedCSID *uuid.UUID, limit, offset int) ([]Ticket, int, error) {
	var tickets []Ticket
	q := r.db.NewSelect().
		Model(&tickets).
		Column("st.*").
		ColumnExpr("u.name AS user_name").
		ColumnExpr("u.email AS user_email").
		ColumnExpr("u.whatsapp_number AS user_whatsapp").
		ColumnExpr("cs.name AS assigned_cs_name").
		ColumnExpr("cs.whatsapp_number AS assigned_cs_whatsapp").
		Join("LEFT JOIN users AS u ON u.id = st.user_id").
		Join("LEFT JOIN users AS cs ON cs.id = st.assigned_cs_id")
	countQ := r.db.NewSelect().Model((*Ticket)(nil))

	if status != "" {
		q = q.Where("st.status = ?", status)
		countQ = countQ.Where("status = ?", status)
	}
	if category != "" {
		q = q.Where("st.category = ?", category)
		countQ = countQ.Where("category = ?", category)
	}
	if assignedCSID != nil {
		q = q.Where("st.assigned_cs_id = ?", *assignedCSID)
		countQ = countQ.Where("assigned_cs_id = ?", *assignedCSID)
	}
	if search != "" {
		// Escape % _ for ILIKE to avoid wildcard abuse / full scan
		escaped := search
		escaped = strings.ReplaceAll(escaped, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `%`, `\%`)
		escaped = strings.ReplaceAll(escaped, `_`, `\_`)
		like := fmt.Sprintf("%%%s%%", escaped)
		q = q.Where("(st.title ILIKE ? OR st.description ILIKE ?)", like, like)
		countQ = countQ.Where("(title ILIKE ? OR description ILIKE ?)", like, like)
	}

	count, err := countQ.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	q = q.Order("st.created_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	err = q.Scan(ctx)
	return tickets, count, err
}

func (r *repository) FindQueueTickets(ctx context.Context, limit, offset int) ([]Ticket, int, error) {
	var tickets []Ticket
	q := r.db.NewSelect().
		Model(&tickets).
		Column("st.*").
		ColumnExpr("u.name AS user_name").
		ColumnExpr("u.email AS user_email").
		ColumnExpr("u.whatsapp_number AS user_whatsapp").
		Join("LEFT JOIN users AS u ON u.id = st.user_id").
		Where("st.status IN ('queued','open')").
		Order("st.created_at ASC")
	countQ := r.db.NewSelect().Model((*Ticket)(nil)).Where("status IN ('queued','open')")

	count, err := countQ.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	err = q.Scan(ctx)
	return tickets, count, err
}

func (r *repository) UpdateTicket(ctx context.Context, ticket *Ticket) error {
	_, err := r.db.NewUpdate().Model(ticket).WherePK().Exec(ctx)
	return err
}

func (r *repository) CountActiveByCS(ctx context.Context, csID uuid.UUID) (int, error) {
	count, err := r.db.NewSelect().Model((*Ticket)(nil)).
		Where("assigned_cs_id = ?", csID).
		Where("status IN ('assigned','in_progress','waiting_user')").
		Count(ctx)
	return count, err
}

func (r *repository) AutoCloseResolved(ctx context.Context, days int) (int, error) {
	res, err := r.db.NewUpdate().Model((*Ticket)(nil)).
		Set("status = 'closed'").
		Set("closed_at = NOW()").
		Set("updated_at = NOW()").
		Where("status = 'resolved'").
		Where("resolved_at < NOW() - (? * INTERVAL '1 day')", days).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	affected, _ := res.RowsAffected()
	return int(affected), nil
}

func (r *repository) GetStats(ctx context.Context) (map[string]int, error) {
	// Single GROUP BY query for prod 512M efficiency
	type statRow struct {
		Status string `bun:"status"`
		Count  int    `bun:"count"`
	}
	var rows []statRow
	err := r.db.NewSelect().
		Model((*Ticket)(nil)).
		ColumnExpr("status").
		ColumnExpr("COUNT(*) as count").
		Group("status").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	stats := map[string]int{
		"queued":       0,
		"open":         0,
		"assigned":     0,
		"in_progress":  0,
		"waiting_user": 0,
		"resolved":     0,
		"closed":       0,
		"total":        0,
	}
	for _, row := range rows {
		stats[row.Status] = row.Count
		stats["total"] += row.Count
	}
	stats["queue"] = stats["queued"] + stats["open"]
	return stats, nil
}

func (r *repository) CreateMessage(ctx context.Context, msg *Message) error {
	_, err := r.db.NewInsert().Model(msg).Exec(ctx)
	return err
}

func (r *repository) FindMessagesByTicketID(ctx context.Context, ticketID uuid.UUID, afterID *uuid.UUID, afterTime *string, limit int, includeInternal bool) ([]Message, error) {
	var msgs []Message
	q := r.db.NewSelect().Model(&msgs).Where("ticket_id = ?", ticketID)

	if !includeInternal {
		q = q.Where("is_internal = false")
	}
	if afterID != nil {
		// Filter by created_at of the target afterID message.
		// Since UUIDs are non-sequential, comparing UUIDs using "id > ?" returns incorrect random results.
		q = q.Where("created_at >= (SELECT created_at FROM support_messages WHERE id = ?)", *afterID).
			Where("id != ?", *afterID)
	}
	if afterTime != nil && *afterTime != "" {
		q = q.Where("created_at > ?", *afterTime)
	}

	q = q.OrderExpr("created_at ASC, id ASC")
	if limit > 0 {
		q = q.Limit(limit)
	} else {
		q = q.Limit(100)
	}
	err := q.Scan(ctx)
	return msgs, err
}

// FAQ

func (r *repository) CreateFAQ(ctx context.Context, faq *FAQ) error {
	_, err := r.db.NewInsert().Model(faq).Exec(ctx)
	return err
}

func (r *repository) UpdateFAQ(ctx context.Context, faq *FAQ) error {
	_, err := r.db.NewUpdate().Model(faq).WherePK().Exec(ctx)
	return err
}

func (r *repository) DeleteFAQ(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.NewDelete().Model((*FAQ)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}

func (r *repository) FindFAQByID(ctx context.Context, id uuid.UUID) (*FAQ, error) {
	f := new(FAQ)
	err := r.db.NewSelect().Model(f).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (r *repository) FindAllFAQ(ctx context.Context, activeOnly bool) ([]FAQ, error) {
	var faqs []FAQ
	q := r.db.NewSelect().Model(&faqs).Order("created_at DESC")
	if activeOnly {
		q = q.Where("is_active = true")
	}
	err := q.Scan(ctx)
	return faqs, err
}

func (r *repository) SearchFAQ(ctx context.Context, query, category string, limit int) ([]FAQ, error) {
	var faqs []FAQ
	q := r.db.NewSelect().Model(&faqs).Where("is_active = true")

	if query != "" {
		escaped := strings.ReplaceAll(query, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `%`, `\%`)
		escaped = strings.ReplaceAll(escaped, `_`, `\_`)
		like := fmt.Sprintf("%%%s%%", escaped)
		q = q.Where("(question ILIKE ? OR answer ILIKE ? OR keywords ILIKE ?)", like, like, like)
	}
	if category != "" {
		q = q.Where("category = ?", category)
	}
	q = q.Order("created_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	} else {
		q = q.Limit(20)
	}
	err := q.Scan(ctx)
	return faqs, err
}
