package kyc

//go:generate mockgen -source=service.go -destination=mocks/service.go -package=mocks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/codecoffy/nitip-core/internal/domain/audit"
	notifDomain "github.com/codecoffy/nitip-core/internal/domain/notification"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/notification"
	"github.com/codecoffy/nitip-core/internal/storage"
	"github.com/codecoffy/nitip-core/pkg/fileutil"
	"github.com/codecoffy/nitip-core/pkg/storageutil"
	"github.com/google/uuid"
	"github.com/uptrace/bun"
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
	TargetLevel            string // separuh or penuh
}

type Service interface {
	Submit(ctx context.Context, userID uuid.UUID, req SubmitKycRequest) (*KycSubmission, error)
	GetStatus(ctx context.Context, userID uuid.UUID) (*KycSubmission, error)
	ListPending(ctx context.Context, offset, limit int) ([]KycSubmission, error)
	Review(ctx context.Context, kycID, actorID uuid.UUID, approved bool, note string) error
	ResetRetry(ctx context.Context, targetUserID uuid.UUID, targetLevel string, actorID uuid.UUID) error
	GetMySubmissions(ctx context.Context, userID uuid.UUID) ([]KycSubmission, int, map[string]int, error)
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
	db            *bun.DB
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

func NewServiceWithDB(repo Repository, userSvc user.Service, storage storage.Storage, fcm notification.Notifier, notifSvc notifDomain.Service, auditSvc audit.Service, db *bun.DB) Service {
	return &service{
		repo:     repo,
		userSvc:  userSvc,
		storage:  storage,
		fcm:      fcm,
		notifSvc: notifSvc,
		auditSvc: auditSvc,
		db:       db,
	}
}

func (s *service) SetFCMDispatcher(d dispatcherIface) {
	s.fcmDispatcher = d
}

func (s *service) enqueueKYC(ctx context.Context, userID uuid.UUID, title, body string, extra map[string]string) {
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
	target := req.TargetLevel
	if target == "" {
		target = LevelSeparuh
	}
	if target != LevelSeparuh && target != LevelPenuh {
		return nil, errors.New("target_level tidak valid")
	}

	// Validate required fields per target
	if target == LevelSeparuh {
		if strings.TrimSpace(req.FacebookName) == "" {
			return nil, errors.New("nama profil facebook wajib diisi")
		}
		if req.FacebookScreenshotFile == nil {
			return nil, errors.New("screenshot facebook wajib diunggah")
		}
		if req.SelfieFile == nil {
			return nil, errors.New("selfie wajib diunggah")
		}
		if req.IdCardFile != nil && strings.TrimSpace(req.IdCardNumber) == "" {
			return nil, errors.New("nomor KTP wajib diisi jika foto KTP disertakan")
		}
	} else {
		// penuh: only KTP required, reuse proven facebook/selfie from separuh
		if strings.TrimSpace(req.IdCardNumber) == "" {
			return nil, errors.New("nomor KTP wajib diisi untuk upgrade penuh")
		}
		if req.IdCardFile == nil {
			return nil, errors.New("foto KTP wajib diunggah untuk upgrade penuh")
		}
	}

	// Load user for level checks
	u, err := s.userSvc.GetByID(ctx, userID, userID)
	if err != nil {
		return nil, err
	}
	currentLevel := u.KycLevel
	if currentLevel == "" {
		if u.IsVerified {
			currentLevel = LevelSeparuh
		} else {
			currentLevel = LevelBelum
		}
	}
	if target == LevelSeparuh {
		if currentLevel == LevelSeparuh || currentLevel == LevelPenuh {
			return nil, errors.New("anda sudah terverifikasi pada level ini atau lebih tinggi")
		}
	} else {
		if currentLevel != LevelSeparuh {
			return nil, errors.New("upgrade KTP hanya tersedia untuk pengguna level separuh")
		}
	}

	// Check pending duplicate per target
	if pend, err := s.repo.GetPendingByUserAndTarget(ctx, userID, target); err == nil && pend != nil {
		return nil, errors.New("pengajuan pending untuk level ini sudah ada")
	}

	// Check rejection limit per target (effective = raw - unlocks)
	rawCnt, _ := s.repo.CountRejectionsByTarget(ctx, userID, target)
	unlocks, _ := s.repo.CountRetryUnlocks(ctx, userID, target)
	effective := rawCnt - unlocks
	if effective < 0 {
		effective = 0
	}
	if effective >= MaxRejections {
		return nil, errors.New("batas percobaan habis, hubungi admin")
	}

	// Upload - track keys for cleanup on DB failure
	var uploadedKeys []string
	cleanup := func() {
		for _, k := range uploadedKeys {
			_ = s.storage.Delete(ctx, storageutil.SanitizeStorageKey(k))
			log.Printf("[KYC] cleanup uploaded file after DB failure: %s", k)
		}
	}

	var idCardPath string
	if req.IdCardFile != nil {
		compressedId, compSize, compErr := fileutil.CompressToLimit(req.IdCardFile, 1600, fileutil.DefaultMaxUpload)
		if compErr != nil {
			return nil, fmt.Errorf("gagal mengompresi KTP: %w", compErr)
		}
		if compSize > fileutil.DefaultMaxUpload {
			return nil, fmt.Errorf("KTP masih >1MB setelah kompresi (%dKB)", compSize/1024)
		}
		idCardKey := fmt.Sprintf("kyc/%s/id_card_%s_%d.jpg", userID.String(), uuid.New().String()[:8], time.Now().UnixNano())
		idCardPath, err = s.storage.Upload(ctx, idCardKey, compressedId, compSize, "image/jpeg")
		if err != nil {
			return nil, err
		}
		uploadedKeys = append(uploadedKeys, idCardPath)
	}

	var selfiePath, facebookScreenshotPath string
	var facebookName string
	if target == LevelSeparuh {
		compressedSelfie, selfieSize, err := fileutil.CompressToLimit(req.SelfieFile, 1200, fileutil.DefaultMaxUpload)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("gagal mengompresi gambar selfie: %w", err)
		}
		if selfieSize > fileutil.DefaultMaxUpload {
			cleanup()
			return nil, fmt.Errorf("selfie masih >1MB setelah kompresi (%dKB)", selfieSize/1024)
		}
		selfieKey := fmt.Sprintf("kyc/%s/selfie_%s_%d.jpg", userID.String(), uuid.New().String()[:8], time.Now().UnixNano())
		selfiePath, err = s.storage.Upload(ctx, selfieKey, compressedSelfie, selfieSize, "image/jpeg")
		if err != nil {
			cleanup()
			return nil, err
		}
		uploadedKeys = append(uploadedKeys, selfiePath)
		compressedFB, fbSize, compErr := fileutil.CompressToLimit(req.FacebookScreenshotFile, 1200, fileutil.DefaultMaxUpload)
		if compErr != nil {
			cleanup()
			return nil, fmt.Errorf("gagal mengompresi screenshot facebook: %w", compErr)
		}
		if fbSize > fileutil.DefaultMaxUpload {
			cleanup()
			return nil, fmt.Errorf("screenshot fb masih >1MB (%dKB)", fbSize/1024)
		}
		fbKey := fmt.Sprintf("kyc/%s/facebook_%s_%d.jpg", userID.String(), uuid.New().String()[:8], time.Now().UnixNano())
		facebookScreenshotPath, err = s.storage.Upload(ctx, fbKey, compressedFB, fbSize, "image/jpeg")
		if err != nil {
			cleanup()
			return nil, err
		}
		uploadedKeys = append(uploadedKeys, facebookScreenshotPath)
		facebookName = req.FacebookName
	} else {
		// penuh: reuse existing proved facebook/selfie, don't require new upload
		selfiePath = ""
		facebookScreenshotPath = ""
		facebookName = ""
	}

	kyc := &KycSubmission{
		ID:                    uuid.New(),
		UserID:                userID,
		IdCardNumber:          req.IdCardNumber,
		IdCardImageURL:        idCardPath,
		SelfieImageURL:        selfiePath,
		FacebookName:          facebookName,
		FacebookScreenshotURL: facebookScreenshotPath,
		Status:                StatusPending,
		TargetLevel:           target,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}

	if err := s.repo.Create(ctx, kyc); err != nil {
		// Unique violation due to concurrent pending -> map to friendly
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") || strings.Contains(strings.ToLower(err.Error()), "unique") {
			cleanup()
			return nil, errors.New("pengajuan pending untuk level ini sudah ada")
		}
		cleanup()
		return nil, err
	}

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

func (s *service) GetMySubmissions(ctx context.Context, userID uuid.UUID) ([]KycSubmission, int, map[string]int, error) {
	subs, err := s.repo.GetByUserID(ctx, userID)
	var all []KycSubmission
	sep, _ := s.repo.GetByUserIDAndTarget(ctx, userID, LevelSeparuh)
	pen, _ := s.repo.GetByUserIDAndTarget(ctx, userID, LevelPenuh)
	all = append(sep, pen...)
	_ = subs
	rawSep, _ := s.repo.CountRejectionsByTarget(ctx, userID, LevelSeparuh)
	unlockSep, _ := s.repo.CountRetryUnlocks(ctx, userID, LevelSeparuh)
	effSep := rawSep - unlockSep
	if effSep < 0 {
		effSep = 0
	}
	rawPen, _ := s.repo.CountRejectionsByTarget(ctx, userID, LevelPenuh)
	unlockPen, _ := s.repo.CountRetryUnlocks(ctx, userID, LevelPenuh)
	effPen := rawPen - unlockPen
	if effPen < 0 {
		effPen = 0
	}
	remaining := map[string]int{
		LevelSeparuh: MaxRejections - effSep,
		LevelPenuh:   MaxRejections - effPen,
	}
	if remaining[LevelSeparuh] < 0 {
		remaining[LevelSeparuh] = 0
	}
	if remaining[LevelPenuh] < 0 {
		remaining[LevelPenuh] = 0
	}
	return all, 0, remaining, err
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
	if !approved && strings.TrimSpace(note) == "" {
		return errors.New("alasan penolakan wajib diisi")
	}
	if s.repo != nil {
		// Try transactional path if available
		if txErr := s.repo.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
			kyc, err := s.repo.GetByIDForUpdate(ctx, tx, kycID)
			if err != nil {
				return err
			}
			if kyc.Status != StatusPending {
				return errors.New("pengajuan sudah diproses")
			}
			if approved {
				// Load user level for update
				uRow, err := s.repo.GetUserForUpdate(ctx, tx, kyc.UserID)
				if err != nil {
					return err
				}
				newLevel := kyc.TargetLevel
				// Don't downgrade: if target separuh and already penuh, keep penuh
				if uRow.KycLevel == LevelPenuh {
					newLevel = LevelPenuh
				} else if kyc.TargetLevel == LevelSeparuh {
					newLevel = LevelSeparuh
					if uRow.KycLevel == LevelPenuh {
						newLevel = LevelPenuh
					}
				} else if kyc.TargetLevel == LevelPenuh {
					newLevel = LevelPenuh
				}
				isVerified := newLevel == LevelSeparuh || newLevel == LevelPenuh
				if err := s.repo.UpdateUserLevelInTx(ctx, tx, kyc.UserID, newLevel, isVerified); err != nil {
					return err
				}
				kyc.Status = StatusApproved
			} else {
				kyc.Status = StatusRejected
			}
			kyc.AdminNote = note
			kyc.UpdatedAt = time.Now()
			if err := s.repo.UpdateInTx(ctx, tx, kyc); err != nil {
				return err
			}
			// Audit inside tx
			action := audit.ActionKYCApproval
			if !approved {
				action = audit.ActionKYCRejection
			}
			s.auditSvc.LogWithDB(ctx, tx, &actorID, action, "kyc", kyc.ID.String(), map[string]interface{}{"status": StatusPending}, map[string]interface{}{"status": kyc.Status, "note": note, "target": kyc.TargetLevel}, "", "")
			return nil
		}); txErr == nil {
			// enqueue after commit
			kyc, _ := s.repo.GetByID(ctx, kycID)
			if kyc != nil {
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
			if approved {
				log.Printf("[ADMIN_ACTION] KYC Approved %s", kycID)
			} else {
				log.Printf("[ADMIN_ACTION] KYC Rejected %s - Note: %s", kycID, note)
			}
			return nil
		} else {
			return txErr
		}
	}
	// fallback non-tx (should not happen)
	return errors.New("review failed")
}

func (s *service) ResetRetry(ctx context.Context, targetUserID uuid.UUID, targetLevel string, actorID uuid.UUID) error {
	if targetLevel != LevelSeparuh && targetLevel != LevelPenuh {
		return errors.New("target_level tidak valid")
	}
	var created bool
	err := s.repo.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		raw, err := s.repo.CountRejectionsByTarget(ctx, targetUserID, targetLevel)
		if err != nil {
			return err
		}
		// Lock unlock rows to prevent concurrent double unlock
		unlocks, err := s.repo.CountRetryUnlocksForUpdate(ctx, tx, targetUserID, targetLevel)
		if err != nil {
			return err
		}
		effective := raw - unlocks
		if effective < 0 {
			effective = 0
		}
		if effective < MaxRejections {
			return errors.New("reset tidak diperlukan, masih ada kesempatan")
		}
		if err := s.repo.CreateRetryUnlockInTx(ctx, tx, targetUserID, targetLevel, actorID); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
				return errors.New("sudah di-unlock, menunggu pengajuan baru")
			}
			return err
		}
		// Audit inside tx
		s.auditSvc.LogWithDB(ctx, tx, &actorID, audit.ActionKYCResetRetry, "kyc", targetUserID.String(), map[string]interface{}{"target_level": targetLevel, "rejections": raw, "effective": effective}, map[string]interface{}{"target_level": targetLevel, "reset": true}, "", "")
		created = true
		return nil
	})
	if err != nil {
		return err
	}
	if !created {
		return errors.New("reset gagal")
	}
	return nil
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
	if kyc.IdCardImageURL != "" {
		key := sanitizeStorageKey(kyc.IdCardImageURL)
		signed, err := s.storage.SignedURL(ctx, key, 1*time.Hour)
		if err == nil {
			kyc.IdCardImageURL = signed
		}
	}
	if kyc.SelfieImageURL != "" {
		key := sanitizeStorageKey(kyc.SelfieImageURL)
		signed, err := s.storage.SignedURL(ctx, key, 1*time.Hour)
		if err == nil {
			kyc.SelfieImageURL = signed
		}
	}
	if kyc.FacebookScreenshotURL != "" {
		key := sanitizeStorageKey(kyc.FacebookScreenshotURL)
		signed, err := s.storage.SignedURL(ctx, key, 1*time.Hour)
		if err == nil {
			kyc.FacebookScreenshotURL = signed
		}
	}
}
