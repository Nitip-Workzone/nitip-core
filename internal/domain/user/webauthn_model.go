package user

import (
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// WebauthnCredential stores the public key and metadata for a WebAuthn credential (passkey).
type WebauthnCredential struct {
	bun.BaseModel `bun:"table:webauthn_credentials,alias:wc"`

	ID              []byte    `bun:"id,pk,type:bytea" json:"id"`
	UserID          uuid.UUID `bun:"user_id,notnull,type:uuid" json:"user_id"`
	PublicKey       []byte    `bun:"public_key,notnull,type:bytea" json:"public_key"`
	AttestationType string    `bun:"attestation_type,notnull" json:"attestation_type"`
	Transport       []string  `bun:"transport,type:varchar[],array,notnull,default:'{}'" json:"transport"`
	AAGUID          []byte    `bun:"aaguid,notnull,type:bytea" json:"aaguid"`
	SignCount       uint32    `bun:"sign_count,notnull,default:0" json:"sign_count"`
	CloneWarning    bool      `bun:"clone_warning,notnull,default:false" json:"clone_warning"`
	CreatedAt       time.Time `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt       time.Time `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`
}

// Convert to WebAuthn internal structure
func (wc *WebauthnCredential) ToWebAuthnCredential() webauthn.Credential {
	transports := make([]protocol.AuthenticatorTransport, len(wc.Transport))
	for i, t := range wc.Transport {
		transports[i] = protocol.AuthenticatorTransport(t)
	}

	return webauthn.Credential{
		ID:              wc.ID,
		PublicKey:       wc.PublicKey,
		AttestationType: wc.AttestationType,
		Transport:       transports,
		Flags: webauthn.CredentialFlags{
			UserPresent:    true,
			UserVerified:   true,
			BackupEligible: true,
			BackupState:    true,
		},
		Authenticator: webauthn.Authenticator{
			AAGUID:       wc.AAGUID,
			SignCount:    wc.SignCount,
			CloneWarning: wc.CloneWarning,
		},
	}
}
