package promotion

import "errors"

var (
	ErrInvalidCodeFormat    = errors.New("format kode voucher tidak valid: 3-50 karakter huruf, angka, - dan _ saja (contoh Merdeka81)")
	ErrCodeAlreadyUsed      = errors.New("kode voucher sudah digunakan")
	ErrInvalidDiscountValue = errors.New("nilai discount tidak valid")
	ErrBudgetTooSmall       = errors.New("budget minimal Rp1.000")
	ErrPromotionNotFound    = errors.New("promosi tidak ditemukan")
	ErrPromotionExpired     = errors.New("promosi sudah berakhir atau belum mulai")
	ErrPromotionInactive    = errors.New("promosi tidak aktif")
	ErrPromotionExhausted   = errors.New("kuota promo habis")
	ErrBudgetExhausted      = errors.New("budget promo habis")
	ErrPerUserLimit         = errors.New("batas penggunaan per user tercapai")
	ErrFirstPurchaseOnly    = errors.New("voucher hanya untuk pembelian pertama")
	ErrMinOrderNotMet       = errors.New("minimal order tidak terpenuhi")
	ErrAutoAndCodeConflict  = errors.New("auto promo tidak boleh memiliki kode voucher")
	ErrNeedCodeOrAuto       = errors.New("harus isi kode voucher atau aktifkan auto first-N")
	ErrNoPromotionFound     = errors.New("tidak ada promo aktif")
)
