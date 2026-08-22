package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// passportAccount is the local Passport profile stored in SQLite.
type passportAccount struct {
	SignIn         string `gorm:"column:sign_in;primaryKey"`
	PassportName   string `gorm:"column:passport_name"`
	Password       string `gorm:"column:password"`
	SecretQuestion string `gorm:"column:secret_question"`
	SecretAnswer   string `gorm:"column:secret_answer"`
	CreatedAt      string `gorm:"column:created_at"`
}

func (passportAccount) TableName() string {
	return "passport_accounts"
}

// passportToken represents a persistent Passport session.
type passportToken struct {
	Token         string `gorm:"column:token;primaryKey"`
	AccountSignIn string `gorm:"column:passport_name"`
	CreatedAt     string `gorm:"column:created_at"`
	ExpiresAt     string `gorm:"column:expires_at"`
}

func (passportToken) TableName() string {
	return "passport_tokens"
}

type accountStore struct {
	db *gorm.DB
}

func openAccountStore(path string, initial passportAccount) (*accountStore, error) {
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("open Passport database: %w", err)
	}

	store := &accountStore{db: db}
	if err := store.migrate(); err != nil {
		_ = store.close()
		return nil, err
	}
	if err := store.seedIfEmpty(initial); err != nil {
		_ = store.close()
		return nil, err
	}
	return store, nil
}

func (s *accountStore) migrate() error {
	if err := s.db.AutoMigrate(&passportAccount{}, &passportToken{}); err != nil {
		return fmt.Errorf("migrate Passport database: %w", err)
	}
	return nil
}

func (s *accountStore) seedIfEmpty(initial passportAccount) error {
	initial.SignIn = strings.TrimSpace(initial.SignIn)
	initial.PassportName = strings.TrimSpace(initial.PassportName)
	if initial.SignIn == "" {
		return nil
	}

	var accountCount int64
	if err := s.db.Model(&passportAccount{}).Count(&accountCount).Error; err != nil {
		return fmt.Errorf("count Passport accounts: %w", err)
	}
	if accountCount > 0 {
		return nil
	}

	initial.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.db.Create(&initial).Error; err != nil {
		return fmt.Errorf("insert initial Passport account: %w", err)
	}
	return nil
}

func (s *accountStore) authenticate(signIn, password string) (passportAccount, bool, error) {
	account, found, err := s.find(signIn)
	if err != nil || !found {
		return passportAccount{}, false, err
	}
	return account, account.Password == password, nil
}

func (s *accountStore) find(signIn string) (passportAccount, bool, error) {
	var account passportAccount
	err := s.db.Where("sign_in = ?", strings.TrimSpace(signIn)).First(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return passportAccount{}, false, nil
	}
	if err != nil {
		return passportAccount{}, false, fmt.Errorf("find Passport account: %w", err)
	}
	return account, true, nil
}

func (s *accountStore) findByPassportName(name string) (passportAccount, bool, error) {
	var account passportAccount
	err := s.db.Where("passport_name = ?", strings.TrimSpace(name)).First(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return passportAccount{}, false, nil
	}
	if err != nil {
		return passportAccount{}, false, fmt.Errorf("find Passport account by name: %w", err)
	}
	return account, true, nil
}

func (s *accountStore) updateAccount(previousSignIn string, account passportAccount) error {
	account.SignIn = strings.TrimSpace(account.SignIn)
	account.PassportName = strings.TrimSpace(account.PassportName)
	if account.SignIn == "" || account.PassportName == "" {
		return fmt.Errorf("email and Passport name are required")
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		var previous passportAccount
		if err := tx.Where("sign_in = ?", previousSignIn).First(&previous).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("Passport account not found")
			}
			return fmt.Errorf("read Passport account: %w", err)
		}

		updates := map[string]interface{}{
			"sign_in":         account.SignIn,
			"passport_name":   account.PassportName,
			"password":        account.Password,
			"secret_question": account.SecretQuestion,
			"secret_answer":   account.SecretAnswer,
		}
		if err := tx.Model(&passportAccount{}).Where("sign_in = ?", previousSignIn).Updates(updates).Error; err != nil {
			return fmt.Errorf("update Passport account: %w", err)
		}

		if err := tx.Model(&passportToken{}).
			Where("passport_name = ? OR passport_name = ?", previousSignIn, previous.PassportName).
			Update("passport_name", account.SignIn).Error; err != nil {
			return fmt.Errorf("update Passport sessions: %w", err)
		}
		return nil
	})
}

func (s *accountStore) saveToken(token, signIn string, expiresAt time.Time) error {
	record := passportToken{
		Token:         token,
		AccountSignIn: signIn,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:     expiresAt.UTC().Format(time.RFC3339),
	}
	if err := s.db.Save(&record).Error; err != nil {
		return fmt.Errorf("store Passport token: %w", err)
	}
	return nil
}

func (s *accountStore) findToken(token string, now time.Time) (string, bool, error) {
	var record passportToken
	err := s.db.Where("token = ? AND expires_at > ?", token, now.UTC().Format(time.RFC3339)).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("find Passport token: %w", err)
	}
	return record.AccountSignIn, true, nil
}

func (s *accountStore) deleteToken(token string) error {
	if err := s.db.Delete(&passportToken{}, "token = ?", token).Error; err != nil {
		return fmt.Errorf("delete Passport token: %w", err)
	}
	return nil
}

func (s *accountStore) close() error {
	if s == nil || s.db == nil {
		return nil
	}
	db, err := s.db.DB()
	if err != nil {
		return err
	}
	return db.Close()
}
