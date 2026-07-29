package support

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/audit"
	notifDomain "github.com/codecoffy/nitip-core/internal/domain/notification"
	"github.com/google/uuid"
)

type Service interface {
	CreateTicket(ctx context.Context, userID uuid.UUID, req CreateTicketRequest) (*Ticket, error)
	GetTicketByID(ctx context.Context, ticketID, requesterID uuid.UUID, isCSOrAdmin bool) (*Ticket, error)
	ListMyTickets(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Ticket, error)
	ListAllTickets(ctx context.Context, status, category, search string, assignedCSID *uuid.UUID, limit, offset int) ([]Ticket, int, error)
	ListQueueTickets(ctx context.Context, limit, offset int) ([]Ticket, int, error)
	GetStats(ctx context.Context) (map[string]int, error)
	ClaimTicket(ctx context.Context, ticketID, csID uuid.UUID) (*Ticket, error)
	ReleaseTicket(ctx context.Context, ticketID, csID uuid.UUID) (*Ticket, error)
	ResolveTicket(ctx context.Context, ticketID, csID uuid.UUID) (*Ticket, error)
	CloseTicket(ctx context.Context, ticketID, userID uuid.UUID, isCS bool) (*Ticket, error)
	ReopenTicket(ctx context.Context, ticketID, userID uuid.UUID) (*Ticket, error)

	SendMessage(ctx context.Context, ticketID, senderID uuid.UUID, senderRole, message string, isInternal bool) (*Message, error)
	GetMessages(ctx context.Context, ticketID uuid.UUID, requesterID uuid.UUID, isCSOrAdmin bool, afterID *uuid.UUID, afterTime *string, limit int) ([]Message, error)

	SearchFAQ(ctx context.Context, query, category string, limit int) ([]FAQ, error)
	ListFAQ(ctx context.Context, activeOnly bool) ([]FAQ, error)
	CreateFAQ(ctx context.Context, req CreateFAQRequest) (*FAQ, error)
	UpdateFAQ(ctx context.Context, id uuid.UUID, req UpdateFAQRequest) (*FAQ, error)
	DeleteFAQ(ctx context.Context, id uuid.UUID) error

	GetActiveTicketByCS(ctx context.Context, csID uuid.UUID) (*Ticket, error)
	StartAutoCloseWorker(ctx context.Context)
}

type service struct {
	repo      Repository
	configSvc ConfigService
	notifSvc  notifDomain.Service
	redis     *cache.Redis
	auditSvc  audit.Service
}

type ConfigService interface {
	GetValue(ctx context.Context, key, defaultValue string) string
}

func NewService(repo Repository, configSvc ConfigService, notifSvc notifDomain.Service, redis *cache.Redis, auditSvc audit.Service) Service {
	return &service{repo: repo, configSvc: configSvc, notifSvc: notifSvc, redis: redis, auditSvc: auditSvc}
}

type CreateTicketRequest struct {
	OrderID     *uuid.UUID `json:"order_id"`
	Category    string     `json:"category" validate:"required,oneof=order_issue payment account merchant other"`
	Title       string     `json:"title" validate:"required,min=5,max=255"`
	Description string     `json:"description" validate:"required,min=10,max=5000"`
	Priority    int        `json:"priority" validate:"omitempty,gte=1,lte=3"`
}

type CreateFAQRequest struct {
	Category string `json:"category" validate:"required"`
	Question string `json:"question" validate:"required,min=5,max=255"`
	Answer   string `json:"answer" validate:"required,min=10"`
	Keywords string `json:"keywords"`
	IsActive *bool  `json:"is_active"`
}

type UpdateFAQRequest struct {
	Category *string `json:"category"`
	Question *string `json:"question" validate:"omitempty,min=5,max=255"`
	Answer   *string `json:"answer" validate:"omitempty,min=10"`
	Keywords *string `json:"keywords"`
	IsActive *bool   `json:"is_active"`
}

func (s *service) CreateTicket(ctx context.Context, userID uuid.UUID, req CreateTicketRequest) (*Ticket, error) {
	now := time.Now()
	t := &Ticket{
		ID:          uuid.New(),
		UserID:      userID,
		OrderID:     req.OrderID,
		Category:    req.Category,
		Title:       req.Title,
		Description: req.Description,
		Status:      StatusQueued,
		Priority:    1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if req.Priority >= 1 && req.Priority <= 3 {
		t.Priority = req.Priority
	}

	if err := s.repo.CreateTicket(ctx, t); err != nil {
		return nil, err
	}

	// Audit
	s.auditSvc.Log(ctx, &userID, "CREATE_SUPPORT_TICKET", "support_ticket", t.ID.String(), nil, t, "", "")

	// Notify admins? For now skip, but could notify CS queue via FCM topic if enabled

	return t, nil
}

func (s *service) GetTicketByID(ctx context.Context, ticketID, requesterID uuid.UUID, isCSOrAdmin bool) (*Ticket, error) {
	t, err := s.repo.FindTicketByID(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if !isCSOrAdmin && t.UserID != requesterID {
		return nil, errors.New("akses ditolak: bukan pemilik tiket")
	}
	return t, nil
}

func (s *service) ListMyTickets(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Ticket, error) {
	return s.repo.FindTicketsByUser(ctx, userID, limit, offset)
}

func (s *service) ListAllTickets(ctx context.Context, status, category, search string, assignedCSID *uuid.UUID, limit, offset int) ([]Ticket, int, error) {
	return s.repo.FindAllTickets(ctx, status, category, search, assignedCSID, limit, offset)
}

func (s *service) ListQueueTickets(ctx context.Context, limit, offset int) ([]Ticket, int, error) {
	return s.repo.FindQueueTickets(ctx, limit, offset)
}

func (s *service) GetActiveTicketByCS(ctx context.Context, csID uuid.UUID) (*Ticket, error) {
	// P1 FIX: was limit 0,0 = no limit fetches ALL tickets for CS (OOM if 10k) -> now limit 20 + filter active in query if repo supports, fallback limited scan
	tickets, _, err := s.repo.FindAllTickets(ctx, "", "", "", &csID, 20, 0)
	if err != nil {
		return nil, err
	}
	for _, t := range tickets {
		if t.Status == StatusAssigned || t.Status == StatusInProgress || t.Status == StatusWaitingUser {
			tt := t
			return &tt, nil
		}
	}
	return nil, nil
}

func (s *service) ClaimTicket(ctx context.Context, ticketID, csID uuid.UUID) (*Ticket, error) {
	// Check max concurrent
	maxStr := s.configSvc.GetValue(ctx, "support_cs_max_concurrent", "1")
	max, _ := strconv.Atoi(maxStr)
	if max <= 0 {
		max = 1
	}

	count, err := s.repo.CountActiveByCS(ctx, csID)
	if err != nil {
		return nil, err
	}
	if count >= max {
		return nil, errors.New("anda sudah menangani 1 sesi aktif, selesaikan dulu sebelum mengambil tiket baru")
	}

	// P2 max perf: use atomic claim to prevent concurrent double claim race (2 CS at same ms)
	// Count check still useful for UI fast-fail, but atomic DB guard is source of truth
	t, err := s.repo.ClaimTicketAtomic(ctx, ticketID, csID)
	if err != nil {
		// Normalize error messages for client
		if err.Error() == "tiket tidak dalam antrian" || err.Error() == "tiket sudah diambil CS lain" || err.Error() == "tiket sudah diambil CS lain (race lost)" {
			return nil, err
		}
		return nil, err
	}

	s.auditSvc.Log(ctx, &csID, "CLAIM_SUPPORT_TICKET", "support_ticket", ticketID.String(), nil, map[string]interface{}{"cs_id": csID, "new_status": StatusInProgress}, "", "")

	// Notify user
	_ = s.notifSvc.CreateNotification(ctx, notifDomain.CreateNotificationRequest{
		UserID:   t.UserID,
		Title:    "Tiket Bantuan Diproses",
		Message:  "CS kami sedang menangani tiket Anda: " + t.Title,
		Type:     "support",
		Metadata: map[string]interface{}{"ticket_id": t.ID.String()},
	})

	return t, nil
}

func (s *service) ReleaseTicket(ctx context.Context, ticketID, csID uuid.UUID) (*Ticket, error) {
	t, err := s.repo.FindTicketByID(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if t.AssignedCSID == nil || *t.AssignedCSID != csID {
		return nil, errors.New("anda bukan pemilik tiket ini")
	}
	if t.Status == StatusResolved || t.Status == StatusClosed {
		return nil, errors.New("tiket sudah selesai, tidak bisa dilepas")
	}

	t.AssignedCSID = nil
	t.Status = StatusQueued
	t.UpdatedAt = time.Now()

	if err := s.repo.UpdateTicket(ctx, t); err != nil {
		return nil, err
	}
	s.auditSvc.Log(ctx, &csID, "RELEASE_SUPPORT_TICKET", "support_ticket", ticketID.String(), nil, t, "", "")
	return t, nil
}

func (s *service) ResolveTicket(ctx context.Context, ticketID, csID uuid.UUID) (*Ticket, error) {
	t, err := s.repo.FindTicketByID(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	// For simplicity, we allow resolve even if assigned check fails for admin case; actual ownership enforcement is in handler role check

	now := time.Now()
	t.Status = StatusResolved
	t.ResolvedAt = &now
	t.UpdatedAt = now

	if err := s.repo.UpdateTicket(ctx, t); err != nil {
		return nil, err
	}

	s.auditSvc.Log(ctx, &csID, "RESOLVE_SUPPORT_TICKET", "support_ticket", ticketID.String(), nil, t, "", "")

	_ = s.notifSvc.CreateNotification(ctx, notifDomain.CreateNotificationRequest{
		UserID:   t.UserID,
		Title:    "Tiket Bantuan Diselesaikan",
		Message:  "Tiket Anda telah diselesaikan oleh CS: " + t.Title,
		Type:     "support",
		Metadata: map[string]interface{}{"ticket_id": t.ID.String()},
	})

	return t, nil
}

func (s *service) CloseTicket(ctx context.Context, ticketID, userID uuid.UUID, isCS bool) (*Ticket, error) {
	t, err := s.repo.FindTicketByID(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if !isCS && t.UserID != userID {
		return nil, errors.New("akses ditolak")
	}

	now := time.Now()
	t.Status = StatusClosed
	t.ClosedAt = &now
	t.UpdatedAt = now

	if err := s.repo.UpdateTicket(ctx, t); err != nil {
		return nil, err
	}
	s.auditSvc.Log(ctx, &userID, "CLOSE_SUPPORT_TICKET", "support_ticket", ticketID.String(), nil, t, "", "")
	return t, nil
}

func (s *service) ReopenTicket(ctx context.Context, ticketID, userID uuid.UUID) (*Ticket, error) {
	t, err := s.repo.FindTicketByID(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if t.UserID != userID {
		return nil, errors.New("akses ditolak")
	}
	if t.Status != StatusResolved && t.Status != StatusClosed {
		return nil, errors.New("hanya tiket resolved/closed yang bisa dibuka kembali")
	}

	t.Status = StatusQueued
	t.AssignedCSID = nil
	t.UpdatedAt = time.Now()
	t.ResolvedAt = nil
	t.ClosedAt = nil

	if err := s.repo.UpdateTicket(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *service) SendMessage(ctx context.Context, ticketID, senderID uuid.UUID, senderRole, message string, isInternal bool) (*Message, error) {
	t, err := s.repo.FindTicketByID(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if t.Status == StatusClosed {
		return nil, errors.New("tiket sudah ditutup, tidak bisa mengirim pesan")
	}

	// Check permission: sender must be ticket owner or assigned CS
	switch senderRole {
	case SenderRoleUser:
		if t.UserID != senderID {
			return nil, errors.New("akses ditolak")
		}
	case SenderRoleCS:
		if t.AssignedCSID == nil || *t.AssignedCSID != senderID {
			return nil, errors.New("anda bukan CS yang menangani tiket ini")
		}
	}

	m := &Message{
		ID:         uuid.New(),
		TicketID:   ticketID,
		SenderID:   senderID,
		SenderRole: senderRole,
		Message:    message,
		IsInternal: isInternal,
		CreatedAt:  time.Now(),
	}

	if err := s.repo.CreateMessage(ctx, m); err != nil {
		return nil, err
	}

	// Update ticket status to reflect activity
	switch senderRole {
	case SenderRoleCS:
		switch t.Status {
		case StatusAssigned:
			t.Status = StatusInProgress
		case StatusInProgress, StatusQueued:
			t.Status = StatusWaitingUser
		}
	case SenderRoleUser:
		if t.Status == StatusWaitingUser {
			t.Status = StatusInProgress
		}
	}
	t.UpdatedAt = time.Now()
	_ = s.repo.UpdateTicket(ctx, t)

	// Notification to opposite party
	if senderRole == SenderRoleCS {
		_ = s.notifSvc.CreateNotification(ctx, notifDomain.CreateNotificationRequest{
			UserID:   t.UserID,
			Title:    "Balasan Baru untuk Tiket Anda",
			Message:  message,
			Type:     "support",
			Metadata: map[string]interface{}{"ticket_id": t.ID.String()},
		})
	} else if senderRole == SenderRoleUser && t.AssignedCSID != nil {
		_ = s.notifSvc.CreateNotification(ctx, notifDomain.CreateNotificationRequest{
			UserID:   *t.AssignedCSID,
			Title:    "Balasan User untuk Tiket",
			Message:  message,
			Type:     "support",
			Metadata: map[string]interface{}{"ticket_id": t.ID.String()},
		})
	}

	return m, nil
}

func (s *service) GetMessages(ctx context.Context, ticketID uuid.UUID, requesterID uuid.UUID, isCSOrAdmin bool, afterID *uuid.UUID, afterTime *string, limit int) ([]Message, error) {
	t, err := s.repo.FindTicketByID(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if !isCSOrAdmin && t.UserID != requesterID {
		return nil, errors.New("akses ditolak")
	}

	includeInternal := isCSOrAdmin
	return s.repo.FindMessagesByTicketID(ctx, ticketID, afterID, afterTime, limit, includeInternal)
}

// FAQ

func (s *service) SearchFAQ(ctx context.Context, query, category string, limit int) ([]FAQ, error) {
	return s.repo.SearchFAQ(ctx, query, category, limit)
}

func (s *service) ListFAQ(ctx context.Context, activeOnly bool) ([]FAQ, error) {
	return s.repo.FindAllFAQ(ctx, activeOnly)
}

func (s *service) CreateFAQ(ctx context.Context, req CreateFAQRequest) (*FAQ, error) {
	now := time.Now()
	faq := &FAQ{
		ID:        uuid.New(),
		Category:  req.Category,
		Question:  req.Question,
		Answer:    req.Answer,
		Keywords:  req.Keywords,
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if req.IsActive != nil {
		faq.IsActive = *req.IsActive
	}
	if err := s.repo.CreateFAQ(ctx, faq); err != nil {
		return nil, err
	}
	return faq, nil
}

func (s *service) UpdateFAQ(ctx context.Context, id uuid.UUID, req UpdateFAQRequest) (*FAQ, error) {
	faq, err := s.repo.FindFAQByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Category != nil {
		faq.Category = *req.Category
	}
	if req.Question != nil {
		faq.Question = *req.Question
	}
	if req.Answer != nil {
		faq.Answer = *req.Answer
	}
	if req.Keywords != nil {
		faq.Keywords = *req.Keywords
	}
	if req.IsActive != nil {
		faq.IsActive = *req.IsActive
	}
	faq.UpdatedAt = time.Now()
	if err := s.repo.UpdateFAQ(ctx, faq); err != nil {
		return nil, err
	}
	return faq, nil
}

func (s *service) DeleteFAQ(ctx context.Context, id uuid.UUID) error {
	return s.repo.DeleteFAQ(ctx, id)
}

func (s *service) GetStats(ctx context.Context) (map[string]int, error) {
	return s.repo.GetStats(ctx)
}

func (s *service) StartAutoCloseWorker(ctx context.Context) {
	go func() {
		// initial delay 10m
		timer := time.NewTimer(10 * time.Minute)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		}
		ticker := time.NewTicker(12 * time.Hour)
		defer ticker.Stop()
		for {
			s.autoCloseResolved(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *service) autoCloseResolved(ctx context.Context) {
	daysStr := s.configSvc.GetValue(ctx, "support_auto_close_days", "7")
	days, err := strconv.Atoi(daysStr)
	if err != nil || days <= 0 {
		return // disabled
	}
	closed, _ := s.repo.AutoCloseResolved(ctx, days)
	if closed > 0 {
		// best effort log
		_ = closed
	}
}
