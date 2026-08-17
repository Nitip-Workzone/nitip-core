package notification

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/audit"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	FCMQueueGlobal    = "fcm:queue:global"
	FCMQueueHigh      = "fcm:queue:high"
	FCMQueueDelayed   = "fcm:delayed"
	FCMDLQ            = "fcm:dlq"
	FCMCounterSent    = "fcm:sent"
	FCMCounterFailed  = "fcm:failed"
	FCMCounterDelayed = "fcm:delayed_per_device"
	FCMCounterInvalid = "fcm:invalid_token"
	FCMBurstLimit     = 20
	FCMBurstWindow    = 10 * time.Minute
	FCMBurstRefill    = 3 * time.Minute
	FCMMaxBatch       = 500
)

type JobPriority string

const (
	PriorityHigh   JobPriority = "high"
	PriorityNormal JobPriority = "normal"
)

type Job struct {
	UserID     uuid.UUID
	Token      string
	Title      string
	Body       string
	Type       string
	Data       map[string]string
	CollapseID string
	Priority   JobPriority
	Retry      int
}

type Dispatcher interface {
	Enqueue(ctx context.Context, job Job) error
	Start(ctx context.Context, workers int)
	QueueDepth(ctx context.Context) (int64, error)
}

type dispatcher struct {
	redis    *cache.Redis
	fcm      Notifier
	userRepo user.Repository
	auditSvc audit.Service
	logger   *zap.Logger
}

func NewDispatcher(redisCache *cache.Redis, fcm Notifier, userRepo user.Repository, auditSvc audit.Service, logger *zap.Logger) Dispatcher {
	return &dispatcher{
		redis:    redisCache,
		fcm:      fcm,
		userRepo: userRepo,
		auditSvc: auditSvc,
		logger:   logger,
	}
}

func (d *dispatcher) Enqueue(ctx context.Context, job Job) error {
	if job.UserID == uuid.Nil && job.Token == "" {
		return fmt.Errorf("job missing userID and token")
	}
	if d.redis == nil {
		return d.sendDirect(ctx, job)
	}
	queueKey := FCMQueueGlobal
	if job.Priority == PriorityHigh {
		queueKey = FCMQueueHigh
	}
	payload, err := marshalJob(job)
	if err != nil {
		return err
	}
	return d.redis.Client().LPush(ctx, queueKey, payload).Err()
}

func (d *dispatcher) Start(ctx context.Context, workers int) {
	if workers <= 0 {
		workers = 5
	}
	for i := 0; i < workers; i++ {
		go d.workerLoop(ctx, i)
	}
	go d.delayedLoop(ctx)
	d.logger.Info("FCM dispatcher started", zap.Int("workers", workers), zap.Int("burst_limit", FCMBurstLimit), zap.Duration("refill", FCMBurstRefill))
}

func (d *dispatcher) workerLoop(ctx context.Context, id int) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		payload, err := d.blockingPop()
		if err != nil {
			if err == redis.Nil {
				time.Sleep(300 * time.Millisecond)
				continue
			}
			d.logger.Error("FCM dispatcher pop error", zap.Error(err), zap.Int("worker", id))
			time.Sleep(1 * time.Second)
			continue
		}
		job, err := unmarshalJob(payload)
		if err != nil {
			d.logger.Error("FCM unmarshal job failed", zap.Error(err))
			continue
		}
		if err := d.processJob(context.Background(), job); err != nil {
			if job.Retry < 3 {
				job.Retry++
				backoff := time.Duration(200*(1<<job.Retry)) * time.Millisecond
				if job.Retry == 2 {
					backoff = 500 * time.Millisecond
				}
				if job.Retry == 3 {
					backoff = 1000 * time.Millisecond
				}
				_ = d.delayEnqueue(context.Background(), job, backoff)
			} else {
				_ = d.redis.Client().LPush(context.Background(), FCMDLQ, payload).Err()
				_ = d.redis.Client().Incr(context.Background(), FCMCounterFailed).Err()
				d.logger.Error("FCM job moved to DLQ", zap.String("collapse", job.CollapseID), zap.Error(err))
			}
		}
	}
}

func (d *dispatcher) blockingPop() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := d.redis.Client().BRPop(ctx, 1*time.Second, FCMQueueHigh, FCMQueueGlobal).Result()
	if err != nil {
		return "", err
	}
	if len(res) < 2 {
		return "", redis.Nil
	}
	return res[1], nil
}

func (d *dispatcher) processJob(ctx context.Context, job Job) error {
	token := job.Token
	if token == "" && job.UserID != uuid.Nil && d.userRepo != nil {
		u, err := d.userRepo.FindByID(ctx, job.UserID)
		if err == nil && u != nil && u.FcmToken != nil && *u.FcmToken != "" {
			token = *u.FcmToken
		} else {
			return nil
		}
	}
	if token == "" {
		return nil
	}

	bucketKey := fmt.Sprintf("fcm:device:%s:count", token)
	count, err := d.redis.Client().Incr(ctx, bucketKey).Result()
	if err == nil {
		if count == 1 {
			_ = d.redis.Client().Expire(ctx, bucketKey, FCMBurstWindow).Err()
		}
		if count > FCMBurstLimit {
			_ = d.delayEnqueue(ctx, job, FCMBurstRefill)
			_ = d.redis.Client().Incr(ctx, FCMCounterDelayed).Err()
			return nil
		}
	}

	err = d.sendDirectWithToken(ctx, token, job)
	if err != nil {
		if isInvalidTokenError(err) {
			_ = d.redis.Client().Incr(ctx, FCMCounterInvalid).Err()
			if d.userRepo != nil && job.UserID != uuid.Nil {
				_ = d.userRepo.Update(ctx, &user.User{ID: job.UserID, FcmToken: nil})
			}
			return nil
		}
		return err
	}

	_ = d.redis.Client().Incr(ctx, FCMCounterSent).Err()
	if d.auditSvc != nil {
		d.auditSvc.Log(context.Background(), &job.UserID, "NOTIFICATION_SENT", "notification", job.UserID.String(), nil, map[string]interface{}{
			"title": job.Title, "type": job.Type, "collapse_id": job.CollapseID, "priority": job.Priority,
		}, "", "")
	}
	return nil
}

type fcmWithCollapse interface {
	SendToDeviceWithCollapse(ctx context.Context, token, title, body string, data map[string]string, collapseID string) error
}

func (d *dispatcher) sendDirect(ctx context.Context, job Job) error {
	if d.fcm == nil {
		return nil
	}
	token := job.Token
	if token == "" && d.userRepo != nil && job.UserID != uuid.Nil {
		u, err := d.userRepo.FindByID(ctx, job.UserID)
		if err == nil && u != nil && u.FcmToken != nil {
			token = *u.FcmToken
		}
	}
	if token == "" {
		return nil
	}
	return d.sendDirectWithToken(ctx, token, job)
}

func (d *dispatcher) sendDirectWithToken(ctx context.Context, token string, job Job) error {
	if d.fcm == nil {
		return nil
	}
	if job.CollapseID != "" {
		if fc, ok := d.fcm.(fcmWithCollapse); ok {
			return fc.SendToDeviceWithCollapse(ctx, token, job.Title, job.Body, job.Data, job.CollapseID)
		}
	}
	return d.fcm.SendToDevice(ctx, token, job.Title, job.Body, job.Data)
}

func (d *dispatcher) delayEnqueue(ctx context.Context, job Job, delay time.Duration) error {
	if d.redis == nil {
		time.Sleep(delay)
		return d.Enqueue(ctx, job)
	}
	payload, err := marshalJob(job)
	if err != nil {
		return err
	}
	score := float64(time.Now().Add(delay).Unix())
	return d.redis.Client().ZAdd(ctx, FCMQueueDelayed, redis.Z{Score: score, Member: payload}).Err()
}

func (d *dispatcher) delayedLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.drainDelayed(context.Background())
		}
	}
}

func (d *dispatcher) drainDelayed(ctx context.Context) {
	if d.redis == nil {
		return
	}
	now := float64(time.Now().Unix())
	members, err := d.redis.Client().ZRangeArgs(ctx, redis.ZRangeArgs{
		Key:     FCMQueueDelayed,
		Start:   "0",
		Stop:    fmt.Sprintf("%f", now),
		ByScore: true,
	}).Result()
	if err != nil || len(members) == 0 {
		return
	}
	for _, m := range members {
		_ = d.redis.Client().ZRem(ctx, FCMQueueDelayed, m).Err()
		_ = d.redis.Client().LPush(ctx, FCMQueueGlobal, m).Err()
	}
}

func (d *dispatcher) QueueDepth(ctx context.Context) (int64, error) {
	if d.redis == nil {
		return 0, nil
	}
	llen, _ := d.redis.Client().LLen(ctx, FCMQueueGlobal).Result()
	zcard, _ := d.redis.Client().ZCard(ctx, FCMQueueDelayed).Result()
	high, _ := d.redis.Client().LLen(ctx, FCMQueueHigh).Result()
	return llen + zcard + high, nil
}

func isInvalidTokenError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "registration-token-not-registered") ||
		strings.Contains(msg, "invalid-argument") && strings.Contains(msg, "token") ||
		strings.Contains(msg, "invalid registration token") ||
		strings.Contains(msg, "requested entity was not found")
}

func marshalJob(job Job) (string, error) {
	return MarshalJobJSON(job)
}
func unmarshalJob(s string) (Job, error) {
	return UnmarshalJobJSON(s)
}
