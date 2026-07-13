package wallet

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Repository interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error

	GetWalletByUserID(ctx context.Context, db bun.IDB, userID uuid.UUID) (*Wallet, error)
	CreateWallet(ctx context.Context, db bun.IDB, wallet *Wallet) error
	GetOrCreateWallet(ctx context.Context, db bun.IDB, userID uuid.UUID) (*Wallet, error)
	UpdateWalletBalance(ctx context.Context, db bun.IDB, walletID uuid.UUID, amount float64) error

	CreateTransaction(ctx context.Context, db bun.IDB, wtx *WalletTransaction) error
	GetTransactionsByWalletID(ctx context.Context, db bun.IDB, walletID uuid.UUID, limit, offset int) ([]WalletTransaction, error)

	// Admin
	GetPendingWithdrawals(ctx context.Context, db bun.IDB, limit, offset int) ([]WalletTransaction, error)
	UpdateTransactionStatus(ctx context.Context, db bun.IDB, id uuid.UUID, status TransactionStatus) error
	GetTransactionByID(ctx context.Context, db bun.IDB, id uuid.UUID) (*WalletTransaction, error)
	GetTransactionByReference(ctx context.Context, db bun.IDB, reference string) (*WalletTransaction, error)
	SumTodayWithdrawals(ctx context.Context, db bun.IDB, walletID uuid.UUID) (float64, error)
	GetActiveWithdrawalChannels(ctx context.Context, db bun.IDB) ([]WithdrawalChannel, error)
	GetWithdrawalChannelByID(ctx context.Context, db bun.IDB, id uuid.UUID) (*WithdrawalChannel, error)
}

type repository struct {
	db *bun.DB
}

func NewRepository(db *bun.DB) Repository {
	return &repository{db: db}
}

func (r *repository) RunInTx(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error {
	return r.db.RunInTx(ctx, nil, fn)
}

func (r *repository) GetWalletByUserID(ctx context.Context, db bun.IDB, userID uuid.UUID) (*Wallet, error) {
	w := new(Wallet)
	err := db.NewSelect().Model(w).Where("user_id = ?", userID).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return w, nil
}

func (r *repository) CreateWallet(ctx context.Context, db bun.IDB, wallet *Wallet) error {
	_, err := db.NewInsert().Model(wallet).Exec(ctx)
	return err
}

func (r *repository) GetOrCreateWallet(ctx context.Context, db bun.IDB, userID uuid.UUID) (*Wallet, error) {
	w := &Wallet{
		ID:      uuid.New(),
		UserID:  userID,
		Balance: 0,
	}

	_, err := db.NewInsert().
		Model(w).
		On("CONFLICT (user_id) DO UPDATE").
		Set("updated_at = EXCLUDED.updated_at").
		Returning("*").
		Exec(ctx)

	if err != nil {
		return nil, err
	}

	return w, nil
}

func (r *repository) UpdateWalletBalance(ctx context.Context, db bun.IDB, walletID uuid.UUID, amount float64) error {
	// Raw SQL SET balance = balance + amount to prevent race conditions during concurrent requests
	// Security guard: (balance + amount) >= 0 to prevent accidental negative balance
	res, err := db.NewUpdate().Model((*Wallet)(nil)).
		Set("balance = balance + ?", amount).
		Set("updated_at = current_timestamp").
		Where("id = ?", walletID).
		Where("balance + ? >= 0", amount).
		Exec(ctx)

	if err == nil {
		rows, _ := res.RowsAffected()
		if rows == 0 && amount < 0 {
			return errors.New("saldo tidak mencukupi (proteksi database)")
		}
	}
	return err
}

func (r *repository) CreateTransaction(ctx context.Context, db bun.IDB, wtx *WalletTransaction) error {
	_, err := db.NewInsert().Model(wtx).Exec(ctx)
	return err
}

func (r *repository) GetTransactionsByWalletID(ctx context.Context, db bun.IDB, walletID uuid.UUID, limit, offset int) ([]WalletTransaction, error) {
	txs := []WalletTransaction{}
	err := db.NewSelect().Model(&txs).
		Where("wallet_id = ?", walletID).
		Order("created_at DESC").
		Limit(limit).Offset(offset).
		Scan(ctx)
	return txs, err
}

func (r *repository) GetPendingWithdrawals(ctx context.Context, db bun.IDB, limit, offset int) ([]WalletTransaction, error) {
	txs := []WalletTransaction{}
	err := db.NewSelect().Model(&txs).
		Where("type = ?", TypeWithdrawal).
		Where("status = ?", StatusPending).
		Order("created_at ASC").
		Limit(limit).Offset(offset).
		Scan(ctx)
	return txs, err
}

func (r *repository) UpdateTransactionStatus(ctx context.Context, db bun.IDB, id uuid.UUID, status TransactionStatus) error {
	_, err := db.NewUpdate().Model((*WalletTransaction)(nil)).
		Set("status = ?", status).
		Where("id = ?", id).
		Exec(ctx)
	return err
}

func (r *repository) GetTransactionByID(ctx context.Context, db bun.IDB, id uuid.UUID) (*WalletTransaction, error) {
	wtx := new(WalletTransaction)
	err := db.NewSelect().Model(wtx).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return wtx, nil
}
func (r *repository) GetTransactionByReference(ctx context.Context, db bun.IDB, reference string) (*WalletTransaction, error) {
	wtx := new(WalletTransaction)
	err := db.NewSelect().Model(wtx).Where("reference = ?", reference).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return wtx, nil
}

func (r *repository) SumTodayWithdrawals(ctx context.Context, db bun.IDB, walletID uuid.UUID) (float64, error) {
	var sum float64
	err := db.NewSelect().
		Model((*WalletTransaction)(nil)).
		ColumnExpr("COALESCE(SUM(ABS(amount)), 0)").
		Where("wallet_id = ?", walletID).
		Where("type = ?", TypeWithdrawal).
		Where("status != ?", "rejected").
		Where("created_at >= CURRENT_DATE").
		Scan(ctx, &sum)
	return sum, err
}

func (r *repository) GetActiveWithdrawalChannels(ctx context.Context, db bun.IDB) ([]WithdrawalChannel, error) {
	var channels []WithdrawalChannel
	err := db.NewSelect().Model(&channels).
		Where("is_active = ?", true).
		Order("type ASC", "name ASC").
		Scan(ctx)
	return channels, err
}

func (r *repository) GetWithdrawalChannelByID(ctx context.Context, db bun.IDB, id uuid.UUID) (*WithdrawalChannel, error) {
	var channel WithdrawalChannel
	err := db.NewSelect().Model(&channel).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &channel, nil
}
