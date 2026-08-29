package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	oauthAuthCodeLifetime    = 5 * time.Minute
	oauthAccessTokenLifetime = time.Hour
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

// oauthClient is a registered partner application.
type oauthClient struct {
	ClientID       string `gorm:"column:client_id;primaryKey"`
	ClientSecret   string `gorm:"column:client_secret_hash"`
	Name           string `gorm:"column:name"`
	OwnerSignIn    string `gorm:"column:owner_sign_in;index"`
	RedirectURIs   string `gorm:"column:redirect_uris"`
	ClassicEnabled bool   `gorm:"column:classic_enabled"`
	Enabled        bool   `gorm:"column:enabled"`
	CreatedAt      string `gorm:"column:created_at"`
}

func (oauthClient) TableName() string {
	return "oauth_clients"
}

// oauthAuthCode is a one-time authorization code.
type oauthAuthCode struct {
	Code          string `gorm:"column:code;primaryKey"`
	ClientID      string `gorm:"column:client_id;index"`
	AccountSignIn string `gorm:"column:account_sign_in"`
	RedirectURI   string `gorm:"column:redirect_uri"`
	ExpiresAt     string `gorm:"column:expires_at"`
	Used          bool   `gorm:"column:used"`
}

func (oauthAuthCode) TableName() string {
	return "oauth_auth_codes"
}

// oauthAccessToken is an opaque OAuth access token.
type oauthAccessToken struct {
	Token         string `gorm:"column:token;primaryKey"`
	ClientID      string `gorm:"column:client_id;index"`
	AccountSignIn string `gorm:"column:account_sign_in"`
	ExpiresAt     string `gorm:"column:expires_at"`
}

func (oauthAccessToken) TableName() string {
	return "oauth_access_tokens"
}

type accountStore struct {
	db     *gorm.DB
	pepper string
	cipher *accountCipher
}

func openAccountStore(path string, initial passportAccount) (*accountStore, error) {
	return openAccountStoreWithPepper(path, initial, "")
}

func openAccountStoreWithPepper(path string, initial passportAccount, pepper string) (*accountStore, error) {
	return openAccountStoreWithCipher(path, initial, pepper, testAccountCipher(pepper))
}

func openAccountStoreWithCipher(path string, initial passportAccount, pepper string, cipher *accountCipher) (*accountStore, error) {
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("open Passport database: %w", err)
	}

	if strings.TrimSpace(pepper) == "" {
		pepper = "lunapassport-lab"
	}
	if cipher == nil {
		return nil, fmt.Errorf("account encryption key is required")
	}
	store := &accountStore{db: db, pepper: pepper, cipher: cipher}
	if err := store.migrate(); err != nil {
		_ = store.close()
		return nil, err
	}
	if err := store.migrateAccountProtection(); err != nil {
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
	if err := s.db.AutoMigrate(
		&passportAccount{},
		&passportToken{},
		&oauthClient{},
		&oauthAuthCode{},
		&oauthAccessToken{},
	); err != nil {
		return fmt.Errorf("migrate Passport database: %w", err)
	}
	return nil
}

func (s *accountStore) migrateAccountProtection() error {
	var accounts []passportAccount
	if err := s.db.Find(&accounts).Error; err != nil {
		return err
	}
	for i := range accounts {
		a := accounts[i]
		if err := s.protectAccount(&a); err != nil {
			return err
		}
		if err := s.db.Model(&passportAccount{}).Where("sign_in = ?", a.SignIn).Updates(map[string]interface{}{"password": a.Password, "secret_question": a.SecretQuestion, "secret_answer": a.SecretAnswer}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *accountStore) hashClientSecret(secret string) string {
	sum := sha256.Sum256([]byte(s.pepper + ":" + secret))
	return hex.EncodeToString(sum[:])
}

func splitRedirectURIs(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func joinRedirectURIs(uris []string) string {
	var cleaned []string
	for _, uri := range uris {
		uri = strings.TrimSpace(uri)
		if uri != "" {
			cleaned = append(cleaned, uri)
		}
	}
	return strings.Join(cleaned, "\n")
}

func (c oauthClient) redirectURIList() []string {
	return splitRedirectURIs(c.RedirectURIs)
}

func (c oauthClient) allowsRedirectURI(uri string) bool {
	uri = strings.TrimSpace(uri)
	for _, allowed := range c.redirectURIList() {
		if allowed == uri {
			return true
		}
	}
	return false
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
	if err := s.protectAccount(&initial); err != nil {
		return err
	}
	if err := s.db.Create(&initial).Error; err != nil {
		return fmt.Errorf("insert initial Passport account: %w", err)
	}
	return nil
}

func (s *accountStore) createAccount(account passportAccount) error {
	account.SignIn = strings.TrimSpace(account.SignIn)
	account.PassportName = strings.TrimSpace(account.PassportName)
	if !strings.Contains(account.SignIn, "@") || account.PassportName == "" {
		return fmt.Errorf("valid e-mail and Passport name are required")
	}
	if len(account.Password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	if account.SecretQuestion == "" || account.SecretAnswer == "" {
		return fmt.Errorf("secret question and answer are required")
	}
	if _, found, err := s.find(account.SignIn); err != nil {
		return err
	} else if found {
		return fmt.Errorf("account already exists")
	}
	account.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.protectAccount(&account); err != nil {
		return err
	}
	return s.db.Create(&account).Error
}

func (s *accountStore) authenticate(signIn, password string) (passportAccount, bool, error) {
	account, found, err := s.find(signIn)
	if err != nil || !found {
		return passportAccount{}, false, err
	}
	if account.Password == "" {
		return account, false, nil
	}
	return account, bcrypt.CompareHashAndPassword([]byte(account.Password), []byte(password)) == nil, nil
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
	if err := s.revealAccount(&account); err != nil {
		return passportAccount{}, false, err
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
	if err := s.revealAccount(&account); err != nil {
		return passportAccount{}, false, err
	}
	return account, true, nil
}

func (s *accountStore) updateAccount(previousSignIn string, account passportAccount) error {
	account.SignIn = strings.TrimSpace(account.SignIn)
	account.PassportName = strings.TrimSpace(account.PassportName)
	if account.SignIn == "" || account.PassportName == "" {
		return fmt.Errorf("email and Passport name are required")
	}
	if err := s.protectAccount(&account); err != nil {
		return err
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
			Update("passport_name", account.PassportName).Error; err != nil {
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

func (s *accountStore) updateTokenIdentity(token, passportName string) error {
	if err := s.db.Model(&passportToken{}).Where("token = ?", token).Update("passport_name", passportName).Error; err != nil {
		return fmt.Errorf("update Passport token: %w", err)
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

func (s *accountStore) createOAuthClient(ownerSignIn, name, secret string, redirectURIs []string, classicEnabled bool) (oauthClient, error) {
	clientID, err := randomToken(16)
	if err != nil {
		return oauthClient{}, fmt.Errorf("generate client id: %w", err)
	}
	client := oauthClient{
		ClientID:       clientID,
		ClientSecret:   s.hashClientSecret(secret),
		Name:           strings.TrimSpace(name),
		OwnerSignIn:    strings.TrimSpace(ownerSignIn),
		RedirectURIs:   joinRedirectURIs(redirectURIs),
		ClassicEnabled: classicEnabled,
		Enabled:        true,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	if client.Name == "" {
		return oauthClient{}, fmt.Errorf("application name is required")
	}
	if len(client.redirectURIList()) == 0 {
		return oauthClient{}, fmt.Errorf("at least one redirect URI is required")
	}
	if err := s.db.Create(&client).Error; err != nil {
		return oauthClient{}, fmt.Errorf("create OAuth client: %w", err)
	}
	return client, nil
}

func (s *accountStore) listOAuthClients() ([]oauthClient, error) {
	var clients []oauthClient
	if err := s.db.Order("created_at desc").Find(&clients).Error; err != nil {
		return nil, fmt.Errorf("list OAuth clients: %w", err)
	}
	return clients, nil
}

func (s *accountStore) findOAuthClient(clientID string) (oauthClient, bool, error) {
	var client oauthClient
	err := s.db.Where("client_id = ?", strings.TrimSpace(clientID)).First(&client).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return oauthClient{}, false, nil
	}
	if err != nil {
		return oauthClient{}, false, fmt.Errorf("find OAuth client: %w", err)
	}
	return client, true, nil
}

func (s *accountStore) authenticateOAuthClient(clientID, secret string) (oauthClient, bool, error) {
	client, found, err := s.findOAuthClient(clientID)
	if err != nil || !found || !client.Enabled {
		return oauthClient{}, false, err
	}
	if client.ClientSecret != s.hashClientSecret(secret) {
		return oauthClient{}, false, nil
	}
	return client, true, nil
}

func (s *accountStore) updateOAuthClientSecret(clientID, secret string) error {
	result := s.db.Model(&oauthClient{}).Where("client_id = ?", clientID).
		Update("client_secret_hash", s.hashClientSecret(secret))
	if result.Error != nil {
		return fmt.Errorf("rotate OAuth client secret: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("OAuth client not found")
	}
	return nil
}

func (s *accountStore) setOAuthClientEnabled(clientID string, enabled bool) error {
	result := s.db.Model(&oauthClient{}).Where("client_id = ?", clientID).Update("enabled", enabled)
	if result.Error != nil {
		return fmt.Errorf("update OAuth client: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("OAuth client not found")
	}
	return nil
}

func (s *accountStore) deleteOAuthClient(clientID string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("client_id = ?", clientID).Delete(&oauthAuthCode{}).Error; err != nil {
			return fmt.Errorf("delete OAuth auth codes: %w", err)
		}
		if err := tx.Where("client_id = ?", clientID).Delete(&oauthAccessToken{}).Error; err != nil {
			return fmt.Errorf("delete OAuth access tokens: %w", err)
		}
		result := tx.Where("client_id = ?", clientID).Delete(&oauthClient{})
		if result.Error != nil {
			return fmt.Errorf("delete OAuth client: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("OAuth client not found")
		}
		return nil
	})
}

func (s *accountStore) isAllowlistedReturnURL(returnURL string) bool {
	returnURL = strings.TrimSpace(returnURL)
	if returnURL == "" {
		return false
	}
	var clients []oauthClient
	if err := s.db.Where("enabled = ? AND classic_enabled = ?", true, true).Find(&clients).Error; err != nil {
		return false
	}
	for _, client := range clients {
		if client.allowsRedirectURI(returnURL) {
			return true
		}
	}
	return false
}

func (s *accountStore) saveOAuthAuthCode(code, clientID, signIn, redirectURI string, expiresAt time.Time) error {
	record := oauthAuthCode{
		Code:          code,
		ClientID:      clientID,
		AccountSignIn: signIn,
		RedirectURI:   redirectURI,
		ExpiresAt:     expiresAt.UTC().Format(time.RFC3339),
		Used:          false,
	}
	if err := s.db.Create(&record).Error; err != nil {
		return fmt.Errorf("store OAuth auth code: %w", err)
	}
	return nil
}

func (s *accountStore) consumeOAuthAuthCode(code, clientID, redirectURI string, now time.Time) (oauthAuthCode, bool, error) {
	var record oauthAuthCode
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("code = ? AND client_id = ? AND used = ? AND expires_at > ?",
			code, clientID, false, now.UTC().Format(time.RFC3339)).First(&record).Error; err != nil {
			return err
		}
		if record.RedirectURI != redirectURI {
			return gorm.ErrRecordNotFound
		}
		if err := tx.Model(&oauthAuthCode{}).Where("code = ?", code).Update("used", true).Error; err != nil {
			return err
		}
		record.Used = true
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return oauthAuthCode{}, false, nil
	}
	if err != nil {
		return oauthAuthCode{}, false, fmt.Errorf("consume OAuth auth code: %w", err)
	}
	return record, true, nil
}

func (s *accountStore) saveOAuthAccessToken(token, clientID, signIn string, expiresAt time.Time) error {
	record := oauthAccessToken{
		Token:         token,
		ClientID:      clientID,
		AccountSignIn: signIn,
		ExpiresAt:     expiresAt.UTC().Format(time.RFC3339),
	}
	if err := s.db.Create(&record).Error; err != nil {
		return fmt.Errorf("store OAuth access token: %w", err)
	}
	return nil
}

func (s *accountStore) findOAuthAccessToken(token string, now time.Time) (oauthAccessToken, bool, error) {
	var record oauthAccessToken
	err := s.db.Where("token = ? AND expires_at > ?", token, now.UTC().Format(time.RFC3339)).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return oauthAccessToken{}, false, nil
	}
	if err != nil {
		return oauthAccessToken{}, false, fmt.Errorf("find OAuth access token: %w", err)
	}
	return record, true, nil
}

func (s *accountStore) deleteOAuthAccessToken(token string) error {
	if err := s.db.Delete(&oauthAccessToken{}, "token = ?", token).Error; err != nil {
		return fmt.Errorf("delete OAuth access token: %w", err)
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
