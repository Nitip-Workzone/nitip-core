package kyc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/codecoffy/nitip-core/internal/domain/audit"
	notifDomain "github.com/codecoffy/nitip-core/internal/domain/notification"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/notification"
	"github.com/codecoffy/nitip-core/internal/storage"
	"github.com/codecoffy/nitip-core/pkg/fileutil"
	"github.com/google/uuid"
)

type SubmitKycRequest struct {
	IdCardNumber           string
	IdCardFile             io.Reader
	IdCardName             string
	SelfieFile             io.Reader
	SelfieName             string
	FacebookName           string
	FacebookScreenshotFile io.Reader
	FacebookScreenshotName string
}

type Service interface {
	Submit(ctx context.Context, userID uuid.UUID, req SubmitKycRequest) (*KycSubmission, error)
	GetStatus(ctx context.Context, userID uuid.UUID) (*KycSubmission, error)
	ListPending(ctx context.Context, offset, limit int) ([]KycSubmission, error)
	Review(ctx context.Context, kycID, actorID uuid.UUID, approved bool, note string) error
}

type dispatcherIface interface {
	Enqueue(ctx context.Context, job notification.Job) error
}

type service struct {
	repo          Repository
	userSvc       user.Service
	storage       storage.Storage
	fcm           notification.Notifier
	fcmDispatcher dispatcherIface
	notifSvc      notifDomain.Service
	auditSvc      audit.Service
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

func (s *service) SetFCMDispatcher(d dispatcherIface) {
	s.fcmDispatcher = d
}

func (s *service) enqueueKYC(ctx context.Context, userID uuid.UUID, title, body string, extra map[string]string) {
	// inbox
	_ = s.notifSvc.CreateNotification(ctx, notifDomain.CreateNotificationRequest{
		UserID:   userID,
		Title:    title,
		Message:  body,
		Type:     "kyc",
		Metadata: map[string]interface{}{"status": extra["status"]},
	})
	if s.fcmDispatcher != nil {
		_ = s.fcmDispatcher.Enqueue(ctx, notification.Job{
			UserID:     userID,
			Title:      title,
			Body:       body,
			Type:       "kyc_result",
			Data:       extra,
			CollapseID: fmt.Sprintf("kyc_%s", userID.String()),
			Priority:   notification.PriorityHigh,
		})
		return
	}
	if s.fcm != nil {
		u, err := s.userSvc.GetByID(ctx, userID, userID)
		if err == nil && u.FcmToken != nil && *u.FcmToken != "" {
			_ = s.fcm.SendToDevice(ctx, *u.FcmToken, title, body, extra)
		}
	}
}

func (s *service) Submit(ctx context.Context, userID uuid.UUID, req SubmitKycRequest) (*KycSubmission, error) {
	// 1. Check if there's already a pending or approved submission
	existing, err := s.repo.GetByUserID(ctx, userID)
	if err == nil && (existing.Status == StatusPending || existing.Status == StatusApproved) {
		return nil, errors.New("anda sudah memiliki pengajuan KYC yang aktif atau tertunda")
	}

	// 2. Upload images to Storage (returns relative path/key)
	var idCardPath string
	if req.IdCardFile != nil {
		var idCardBuf bytes.Buffer
		idCardSize, err := io.Copy(&idCardBuf, req.IdCardFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read id card file: %w", err)
		}
		idCardContentType := "image/jpeg"
		idCardLimit := 512
		if idCardBuf.Len() < idCardLimit {
			idCardLimit = idCardBuf.Len()
		}
		if idCardLimit > 0 {
			idCardContentType = http.DetectContentType(idCardBuf.Bytes()[:idCardLimit])
			if idCardContentType == "application/octet-stream" {
				idCardContentType = "image/jpeg"
			}
		}
		// Cache-busting: tiap re-upload nama unik baru agar CDN https://upload.nihtip.com/ tidak cache file lama
		idCardKey := fmt.Sprintf("kyc/%s/id_card_%s_%d.jpg", userID.String(), uuid.New().String()[:8], time.Now().UnixNano())
		idCardPath, err = s.storage.Upload(ctx, idCardKey, &idCardBuf, idCardSize, idCardContentType)
		if err != nil {
			return nil, err
		}
	}

	// Selfie image compression
	compressedSelfie, err := fileutil.CompressAndResizeImage(req.SelfieFile, 1200, 75)
	if err != nil {
		return nil, fmt.Errorf("gagal mengompresi gambar selfie: %w", err)
	}
	var selfieBuf bytes.Buffer
	selfieSize, err := io.Copy(&selfieBuf, compressedSelfie)
	if err != nil {
		return nil, fmt.Errorf("failed to read compressed selfie: %w", err)
	}
	selfieContentType := "image/jpeg"
	selfieKey := fmt.Sprintf("kyc/%s/selfie_%s_%d.jpg", userID.String(), uuid.New().String()[:8], time.Now().UnixNano())
	selfiePath, err := s.storage.Upload(ctx, selfieKey, &selfieBuf, selfieSize, selfieContentType)
	if err != nil {
		return nil, err
	}

	// Facebook screenshot compression and upload
	var facebookScreenshotPath string
	if req.FacebookScreenshotFile != nil {
		compressedFB, err := fileutil.CompressAndResizeImage(req.FacebookScreenshotFile, 1200, 75)
		if err != nil {
			return nil, fmt.Errorf("gagal mengompresi screenshot facebook: %w", err)
		}
		var fbBuf bytes.Buffer
		fbSize, err := io.Copy(&fbBuf, compressedFB)
		if err != nil {
			return nil, fmt.Errorf("failed to read compressed facebook screenshot: %w", err)
		}
		fbContentType := "image/jpeg"
		fbKey := fmt.Sprintf("kyc/%s/facebook_%s_%d.jpg", userID.String(), uuid.New().String()[:8], time.Now().UnixNano())
		facebookScreenshotPath, err = s.storage.Upload(ctx, fbKey, &fbBuf, fbSize, fbContentType)
		if err != nil {
			return nil, err
		}
	}

	// 3. Create submission record
	kyc := &KycSubmission{
		ID:                    uuid.New(),
		UserID:                userID,
		IdCardNumber:          req.IdCardNumber,
		IdCardImageURL:        idCardPath,
		SelfieImageURL:        selfiePath,
		FacebookName:          req.FacebookName,
		FacebookScreenshotURL: facebookScreenshotPath,
		Status:                StatusPending,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
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
		return errors.New("pengajuan sudah diproses")
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
	if err == nil {
		title := "Verifikasi Identitas Selesai"
		body := "Selamat! Identitas Anda telah berhasil diverifikasi."
		if !approved {
			title = "Verifikasi Identitas Ditolak"
			body = "Mohon maaf, verifikasi Identitas Anda ditolak. Alasan: " + note
		}
		s.enqueueKYC(ctx, kyc.UserID, title, body, map[string]string{
			"type":   "kyc_result",
			"status": kyc.Status,
		})
	}

	return err
}

func sanitizeStorageKey(urlStr string) string {
	if urlStr == "" {
		return ""
	}
	if strings.HasPrefix(urlStr, "http://") || strings.HasPrefix(urlStr, "https://") {
		temp := urlStr
		if strings.HasPrefix(temp, "https://") {
			temp = strings.TrimPrefix(temp, "https://")
		} else {
			temp = strings.TrimPrefix(temp, "http://")
		}

		slashIdx := strings.Index(temp, "/")
		if slashIdx != -1 {
			path := temp[slashIdx+1:]
			path = strings.TrimPrefix(path, "uploads/")

			// Strip query parameters
			if qIdx := strings.Index(path, "?"); qIdx != -1 {
				path = path[:qIdx]
			}
			return path
		}
	}
	return urlStr
}

func (s *service) signURLs(ctx context.Context, kyc *KycSubmission) {
	if kyc == nil {
		return
	}
	// Sign IdCardImageURL
	if kyc.IdCardImageURL != "" {
		key := sanitizeStorageKey(kyc.IdCardImageURL)
		signed, err := s.storage.SignedURL(ctx, key, 1*time.Hour)
		if err == nil {
			kyc.IdCardImageURL = signed
		}
	}
	// Sign SelfieImageURL
	if kyc.SelfieImageURL != "" {
		key := sanitizeStorageKey(kyc.SelfieImageURL)
		signed, err := s.storage.SignedURL(ctx, key, 1*time.Hour)
		if err == nil {
			kyc.SelfieImageURL = signed
		}
	}
	// Sign FacebookScreenshotURL
	if kyc.FacebookScreenshotURL != "" {
		key := sanitizeStorageKey(kyc.FacebookScreenshotURL)
		signed, err := s.storage.SignedURL(ctx, key, 1*time.Hour)
		if err == nil {
			kyc.FacebookScreenshotURL = signed
		}
	}
}
