package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/icco/art/lib/models"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store persists linked accounts and their sealed refresh tokens.
type Store struct {
	DB     *gorm.DB
	Sealer *Sealer
}

// Save upserts an Account row, sealing the refresh token before write.
func (s *Store) Save(ctx context.Context, kind models.AccountKind, email, primaryCal string, tok *oauth2.Token) error {
	if tok.RefreshToken == "" {
		return errors.New("oauth: refresh token missing — revoke and retry with prompt=consent")
	}
	// Access token is short-lived; TokenSource refreshes on first use.
	// #nosec G117 -- payload is sealed by AES-256-GCM before persistence.
	payload, err := json.Marshal(&oauth2.Token{RefreshToken: tok.RefreshToken})
	if err != nil {
		return err
	}
	sealed, err := s.Sealer.Seal(payload)
	if err != nil {
		return err
	}
	a := models.Account{
		Kind:                  kind,
		Email:                 email,
		RefreshTokenEncrypted: sealed,
		PrimaryCalendarID:     primaryCal,
	}
	return s.DB.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "kind"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"email", "refresh_token_encrypted", "primary_calendar_id", "updated_at",
			}),
		}).
		Create(&a).Error
}

// UpdateRefreshToken reseals and stores a rotated refresh token for kind.
func (s *Store) UpdateRefreshToken(ctx context.Context, kind models.AccountKind, refresh string) error {
	if refresh == "" {
		return errors.New("oauth: refresh token missing")
	}
	// #nosec G117 -- payload is sealed by AES-256-GCM before persistence.
	payload, err := json.Marshal(&oauth2.Token{RefreshToken: refresh})
	if err != nil {
		return err
	}
	sealed, err := s.Sealer.Seal(payload)
	if err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Model(&models.Account{}).
		Where("kind = ?", kind).
		Update("refresh_token_encrypted", sealed).Error
}

// Load returns the decrypted oauth2.Token and Account row for kind.
func (s *Store) Load(ctx context.Context, kind models.AccountKind) (*oauth2.Token, models.Account, error) {
	var a models.Account
	if err := s.DB.WithContext(ctx).Where("kind = ?", kind).First(&a).Error; err != nil {
		return nil, a, err
	}
	plain, err := s.Sealer.Open(a.RefreshTokenEncrypted)
	if err != nil {
		return nil, a, fmt.Errorf("oauth: decrypt: %w", err)
	}
	var tok oauth2.Token
	if err := json.Unmarshal(plain, &tok); err != nil {
		return nil, a, err
	}
	return &tok, a, nil
}

// All returns all linked accounts in deterministic order.
func (s *Store) All(ctx context.Context) ([]models.Account, error) {
	var out []models.Account
	err := s.DB.WithContext(ctx).Order("kind").Find(&out).Error
	return out, err
}
