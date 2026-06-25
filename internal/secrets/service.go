package secrets

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"quack/internal/domain"
	"quack/internal/sites"
)

const (
	rootKeyBytes = 32
	saltBytes    = 16
	nonceBytes   = 12
	kdfPBKDF2    = "pbkdf2-sha256"
	defaultIters = 310000
	defaultKeyID = "site-admin"
)

type UnlockKeyRecord struct {
	KeyID            string
	KDF              string
	Iterations       int
	Salt             []byte
	Nonce            []byte
	EncryptedRootKey []byte
	CreatedByUserID  int64
	CreatedAt        string
	UpdatedAt        string
}

type SecretRecord struct {
	Scope           domain.SecretScope
	ScopeID         string
	Name            string
	Nonce           []byte
	Ciphertext      []byte
	CreatedByUserID int64
	CreatedAt       string
	UpdatedAt       string
}

type SecretSummary struct {
	Scope     domain.SecretScope
	Site      string
	Name      string
	CreatedAt string
	UpdatedAt string
}

type Repository interface {
	LoadUnlockKeys(ctx context.Context) ([]UnlockKeyRecord, error)
	SaveUnlockKey(ctx context.Context, record UnlockKeyRecord) error
	UpsertSecret(ctx context.Context, record SecretRecord) error
	GetSecret(ctx context.Context, scope domain.SecretScope, scopeID string, name string) (SecretRecord, bool, error)
	ListSecretsForUser(ctx context.Context, userID int64, siteSHA string) ([]SecretRecord, error)
	DeleteSecretForUser(ctx context.Context, userID int64, scope domain.SecretScope, scopeID string, name string) (bool, error)
	SiteExists(ctx context.Context, siteSHA string) (bool, error)
	UserCanAccessSite(ctx context.Context, userID int64, siteSHA string) (bool, error)
}

type Service struct {
	repo Repository
	mu   sync.RWMutex
	root []byte
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Status(ctx context.Context) (hasKey bool, unlocked bool, err error) {
	keys, err := s.repo.LoadUnlockKeys(ctx)
	if err != nil {
		return false, false, err
	}
	return len(keys) > 0, s.Unlocked(), nil
}

func (s *Service) Unlocked() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.root) == rootKeyBytes
}

func (s *Service) Initialize(ctx context.Context, passphrase string, userID int64) error {
	passphrase = strings.TrimSpace(passphrase)
	if passphrase == "" {
		return fmt.Errorf("passphrase is required")
	}
	keys, err := s.repo.LoadUnlockKeys(ctx)
	if err != nil {
		return err
	}
	if len(keys) > 0 {
		return fmt.Errorf("root key already exists")
	}
	root, err := randomBytes(rootKeyBytes)
	if err != nil {
		return err
	}
	record, err := wrapRootKey(root, passphrase, userID)
	if err != nil {
		return err
	}
	if err := s.repo.SaveUnlockKey(ctx, record); err != nil {
		return err
	}
	s.setRoot(root)
	return nil
}

func (s *Service) Unlock(ctx context.Context, passphrase string) error {
	root, err := s.unwrapStoredRoot(ctx, passphrase)
	if err != nil {
		return err
	}
	s.setRoot(root)
	return nil
}

func (s *Service) ResetPassphrase(ctx context.Context, oldPassphrase string, newPassphrase string, userID int64) error {
	newPassphrase = strings.TrimSpace(newPassphrase)
	if newPassphrase == "" {
		return fmt.Errorf("new passphrase is required")
	}
	root, err := s.unwrapStoredRoot(ctx, oldPassphrase)
	if err != nil {
		return err
	}
	current := s.rootCopy()
	if len(current) != rootKeyBytes || !bytes.Equal(current, root) {
		return domain.ErrSecretsLocked
	}
	record, err := wrapRootKey(root, newPassphrase, userID)
	if err != nil {
		return err
	}
	if err := s.repo.SaveUnlockKey(ctx, record); err != nil {
		return err
	}
	s.setRoot(root)
	return nil
}

func (s *Service) Set(ctx context.Context, user domain.AdminUser, site string, scope domain.SecretScope, name string, value string) error {
	scopeID, err := s.scopeID(ctx, user, site, scope)
	if err != nil {
		return err
	}
	name = normalizeName(name)
	if name == "" {
		return fmt.Errorf("secret name is required")
	}
	root := s.rootCopy()
	if len(root) != rootKeyBytes {
		return domain.ErrSecretsLocked
	}
	nonce, ciphertext, err := encrypt(root, []byte(value), []byte(secretAAD(scope, scopeID, name)))
	if err != nil {
		return err
	}
	return s.repo.UpsertSecret(ctx, SecretRecord{
		Scope: scope, ScopeID: scopeID, Name: name, Nonce: nonce, Ciphertext: ciphertext, CreatedByUserID: user.ID,
	})
}

func (s *Service) Get(ctx context.Context, site string, scope domain.SecretScope, name string) (string, error) {
	scopeID, err := runtimeScopeID(site, scope)
	if err != nil {
		return "", err
	}
	name = normalizeName(name)
	if name == "" {
		return "", fmt.Errorf("secret name is required")
	}
	root := s.rootCopy()
	if len(root) != rootKeyBytes {
		return "", domain.ErrSecretsLocked
	}
	record, ok, err := s.repo.GetSecret(ctx, scope, scopeID, name)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("secret not found")
	}
	plaintext, err := decrypt(root, record.Nonce, record.Ciphertext, []byte(secretAAD(record.Scope, record.ScopeID, record.Name)))
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func (s *Service) Available(ctx context.Context, site string, scope domain.SecretScope, name string) (bool, error) {
	scopeID, err := runtimeScopeID(site, scope)
	if err != nil {
		return false, err
	}
	name = normalizeName(name)
	if name == "" {
		return false, fmt.Errorf("secret name is required")
	}
	root := s.rootCopy()
	if len(root) != rootKeyBytes {
		return false, nil
	}
	_, ok, err := s.repo.GetSecret(ctx, scope, scopeID, name)
	if err != nil {
		return false, err
	}
	return ok, nil
}

func (s *Service) List(ctx context.Context, user domain.AdminUser, site string) ([]SecretSummary, error) {
	siteSHA := ""
	if strings.TrimSpace(site) != "" {
		var err error
		siteSHA, err = siteScopeID(ctx, s.repo, user, site)
		if err != nil {
			return nil, err
		}
	}
	records, err := s.repo.ListSecretsForUser(ctx, user.ID, siteSHA)
	if err != nil {
		return nil, err
	}
	out := make([]SecretSummary, 0, len(records))
	for _, record := range records {
		out = append(out, SecretSummary{
			Scope: record.Scope, Site: site, Name: record.Name, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
		})
	}
	return out, nil
}

func (s *Service) Delete(ctx context.Context, user domain.AdminUser, site string, scope domain.SecretScope, name string) (bool, error) {
	scopeID, err := s.scopeID(ctx, user, site, scope)
	if err != nil {
		return false, err
	}
	name = normalizeName(name)
	if name == "" {
		return false, fmt.Errorf("secret name is required")
	}
	return s.repo.DeleteSecretForUser(ctx, user.ID, scope, scopeID, name)
}

func (s *Service) unwrapStoredRoot(ctx context.Context, passphrase string) ([]byte, error) {
	keys, err := s.repo.LoadUnlockKeys(ctx)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("root key has not been created")
	}
	// TODO: try all configured admin unlock keys once the UI supports multiple key records.
	return unwrapRootKey(keys[0], passphrase)
}

func (s *Service) setRoot(root []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.root = append(s.root[:0], root...)
}

func (s *Service) rootCopy() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]byte(nil), s.root...)
}

func (s *Service) scopeID(ctx context.Context, user domain.AdminUser, site string, scope domain.SecretScope) (string, error) {
	switch scope {
	case domain.SecretScopeSite:
		return siteScopeID(ctx, s.repo, user, site)
	case domain.SecretScopeUser:
		if user.ID <= 0 {
			return "", fmt.Errorf("user is required")
		}
		return strconv.FormatInt(user.ID, 10), nil
	default:
		return "", fmt.Errorf("secret scope must be site or user")
	}
}

func siteScopeID(ctx context.Context, repo Repository, user domain.AdminUser, site string) (string, error) {
	site, err := sites.CanonicalName(site)
	if err != nil {
		return "", err
	}
	siteSHA := sites.HashName(site)
	exists, err := repo.SiteExists(ctx, siteSHA)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("site does not exist")
	}
	if !user.IsAdmin() {
		ok, err := repo.UserCanAccessSite(ctx, user.ID, siteSHA)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", domain.ErrSiteOwnership
		}
	}
	return siteSHA, nil
}

func runtimeScopeID(site string, scope domain.SecretScope) (string, error) {
	switch scope {
	case domain.SecretScopeSite:
		site, err := sites.CanonicalName(site)
		if err != nil {
			return "", err
		}
		return sites.HashName(site), nil
	case domain.SecretScopeUser:
		return "", fmt.Errorf("user-scoped secrets require user context")
	default:
		return "", fmt.Errorf("secret scope must be site or user")
	}
}

func wrapRootKey(root []byte, passphrase string, userID int64) (UnlockKeyRecord, error) {
	salt, err := randomBytes(saltBytes)
	if err != nil {
		return UnlockKeyRecord{}, err
	}
	key, err := deriveKey(passphrase, salt, defaultIters)
	if err != nil {
		return UnlockKeyRecord{}, err
	}
	nonce, ciphertext, err := encrypt(key, root, []byte(defaultKeyID))
	if err != nil {
		return UnlockKeyRecord{}, err
	}
	return UnlockKeyRecord{
		KeyID: defaultKeyID, KDF: kdfPBKDF2, Iterations: defaultIters, Salt: salt,
		Nonce: nonce, EncryptedRootKey: ciphertext, CreatedByUserID: userID,
	}, nil
}

func unwrapRootKey(record UnlockKeyRecord, passphrase string) ([]byte, error) {
	if record.KDF != "" && record.KDF != kdfPBKDF2 {
		return nil, fmt.Errorf("unsupported unlock key kdf")
	}
	iters := record.Iterations
	if iters <= 0 {
		iters = defaultIters
	}
	key, err := deriveKey(passphrase, record.Salt, iters)
	if err != nil {
		return nil, err
	}
	root, err := decrypt(key, record.Nonce, record.EncryptedRootKey, []byte(record.KeyID))
	if err != nil {
		return nil, fmt.Errorf("invalid passphrase")
	}
	if len(root) != rootKeyBytes {
		return nil, fmt.Errorf("stored root key is invalid")
	}
	return root, nil
}

func deriveKey(passphrase string, salt []byte, iterations int) ([]byte, error) {
	passphrase = strings.TrimSpace(passphrase)
	if passphrase == "" {
		return nil, fmt.Errorf("passphrase is required")
	}
	return pbkdf2.Key(sha256.New, passphrase, salt, iterations, rootKeyBytes)
}

func encrypt(key []byte, plaintext []byte, aad []byte) ([]byte, []byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce, err := randomBytes(nonceBytes)
	if err != nil {
		return nil, nil, err
	}
	return nonce, gcm.Seal(nil, nonce, plaintext, aad), nil
}

func decrypt(key []byte, nonce []byte, ciphertext []byte, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, aad)
}

func randomBytes(n int) ([]byte, error) {
	out := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, out); err != nil {
		return nil, err
	}
	return out, nil
}

func secretAAD(scope domain.SecretScope, scopeID string, name string) string {
	return string(scope) + "\x00" + scopeID + "\x00" + name
}

func normalizeName(name string) string {
	return strings.TrimSpace(name)
}

func IsLocked(err error) bool {
	return errors.Is(err, domain.ErrSecretsLocked)
}
