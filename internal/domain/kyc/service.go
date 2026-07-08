package kyc

import (
	"context"
	"errors"
	"io"
	"log"
	"time"

	"github.com/codecoffy/nitip-core/internal/domain/audit"
	notifDomain "github.com/codecoffy/nitip-core/internal/domain/notification"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/infrastructure/storage"
	"github.com/codecoffy/nitip-core/internal/notification"
	"github.com/google/uuid"
)

type SubmitKycRequest struct {
	IdCardNumber string
	IdCardFile   io.Reader
	IdCardName   string
	SelfieFile   io.Reader
	SelfieName   string
}

type Service interface {
	Submit(ctx context.Context, userID uuid.UUID, req SubmitKycRequest) (*KycSubmission, error)
	GetStatus(ctx context.Context, userID uuid.UUID) (*KycSubmission, error)
	ListPending(ctx context.Context, offset, limit int) ([]KycSubmission, error)
	Review(ctx context.Context, kycID, actorID uuid.UUID, approved bool, note string) error
}

type service struct {
	repo     Repository
	userSvc  user.Service
	storage  storage.Storage
	fcm      notification.Notifier
	notifSvc notifDomain.Service
	auditSvc audit.Service
}

func NewService(repo Repository, userSvc user.Service, storage storage.Storage, fcm notification.Notifier, notifSvc notifDomain.Service, auditSvc audit.Service) Service {
	return &service{
		repo:     repo,
		userSvc:  userSvc,
		storage:  storage,
		fcm:      fcm,
		notifSvc: notifSvc,
		auditSvc: auditSvc,
	}
}

func (s *service) Submit(ctx context.Context, userID uuid.UUID, req SubmitKycRequest) (*KycSubmission, error) {
	// 1. Check if there's already a pending or approved submission
	existing, err := s.repo.GetByUserID(ctx, userID)
	if err == nil && (existing.Status == StatusPending || existing.Status == StatusApproved) {
		return nil, errors.New("you already have an active or pending kyc submission")
	}

	// 2. Upload images to Storage (returns relative path/key)
	folder := "kyc/" + userID.String()
	idCardPath, err := s.storage.Upload(ctx, folder, "id_card_"+req.IdCardName, req.IdCardFile)
	if err != nil {
		return nil, err
	}

	selfiePath, err := s.storage.Upload(ctx, folder, "selfie_"+req.SelfieName, req.SelfieFile)
	if err != nil {
		return nil, err
	}

	// 3. Create submission record
	kyc := &KycSubmission{
		ID:             uuid.New(),
		UserID:         userID,
		IdCardNumber:   req.IdCardNumber,
		IdCardImageURL: idCardPath, // Storing path
		SelfieImageURL: selfiePath, // Storing path
		Status:         StatusPending,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := s.repo.Create(ctx, kyc); err != nil {
		return nil, err
	}

	// 4. Sign URLs before returning to user
	s.signURLs(ctx, kyc)

	return kyc, nil
}

func (s *service) GetStatus(ctx context.Context, userID uuid.UUID) (*KycSubmission, error) {
	kyc, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	s.signURLs(ctx, kyc)
	return kyc, nil
}

func (s *service) ListPending(ctx context.Context, offset, limit int) ([]KycSubmission, error) {
	submissions, err := s.repo.ListPending(ctx, offset, limit)
	if err != nil {
		return nil, err
	}
	for i := range submissions {
		s.signURLs(ctx, &submissions[i])
	}
	return submissions, nil
}

func (s *service) Review(ctx context.Context, kycID, actorID uuid.UUID, approved bool, note string) error {
	kyc, err := s.repo.GetByID(ctx, kycID)
	if err != nil {
		return err
	}

	if kyc.Status != StatusPending {
		return errors.New("submission is already processed")
	}

	if approved {
		kyc.Status = StatusApproved
		if err := s.userSvc.UpdateVerification(ctx, kyc.UserID, actorID, true); err != nil {
			return err
		}
		log.Printf("[ADMIN_ACTION] KYC Approved for User %s", kyc.UserID)
	} else {
		kyc.Status = StatusRejected
		log.Printf("[ADMIN_ACTION] KYC Rejected for User %s - Note: %s", kyc.UserID, note)
	}

	kyc.AdminNote = note
	kyc.UpdatedAt = time.Now()

	err = s.repo.Update(ctx, kyc)
	if err == nil {
		action := audit.ActionKYCApproval
		if !approved {
			action = audit.ActionKYCRejection
		}
		// Use actorID for audit log
		s.auditSvc.Log(ctx, &actorID, action, "kyc", kyc.ID.String(), map[string]interface{}{"status": StatusPending}, map[string]interface{}{"status": kyc.Status, "note": note}, "", "")
	}
	if err == nil && s.fcm != nil {
		// Notify User
		u, errUser := s.userSvc.GetByID(ctx, kyc.UserID, kyc.UserID)
		if errUser == nil && u.FcmToken != nil && *u.FcmToken != "" {
			title := "Verifikasi Identitas Selesai"
			body := "Selamat! Identitas Anda telah berhasil diverifikasi."
			if !approved {
				title = "Verifikasi Identitas Ditolak"
				body = "Mohon maaf, verifikasi Identitas Anda ditolak. Alasan: " + note
			}

			_ = s.fcm.SendToDevice(ctx, *u.FcmToken, title, body, map[string]string{
				"type":   "kyc_result",
				"status": kyc.Status,
			})
		}

		// Create In-App Notification
		title := "Verifikasi Identitas Selesai"
		body := "Selamat! Identitas Anda telah berhasil diverifikasi."
		if !approved {
			title = "Verifikasi Identitas Ditolak"
			body = "Mohon maaf, verifikasi Identitas Anda ditolak. Alasan: " + note
		}
		_ = s.notifSvc.CreateNotification(ctx, notifDomain.CreateNotificationRequest{
			UserID:  kyc.UserID,
			Title:   title,
			Message: body,
			Type:    "kyc",
			Metadata: map[string]interface{}{
				"status": kyc.Status,
			},
		})
	}

	return err
}
func (s *service) signURLs(ctx context.Context, kyc *KycSubmission) {
	if kyc == nil {
		return
	}
	// Sign IdCardImageURL
	if kyc.IdCardImageURL != "" {
		signed, err := s.storage.GetSignedURL(ctx, kyc.IdCardImageURL, 1*time.Hour)
		if err == nil {
			kyc.IdCardImageURL = signed
		}
	}
	// Sign SelfieImageURL
	if kyc.SelfieImageURL != "" {
		signed, err := s.storage.GetSignedURL(ctx, kyc.SelfieImageURL, 1*time.Hour)
		if err == nil {
			kyc.SelfieImageURL = signed
		}
	}
}
