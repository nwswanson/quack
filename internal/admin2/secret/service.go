package secret

import (
	"context"
	"errors"
	"strings"
)

var ErrPassphraseRequired = errors.New("passphrase required")

type SecretStore interface {
	Unlocked() bool
	Unlock(ctx context.Context, passphrase string) error
}

type Service struct {
	secrets SecretStore
}

func NewService(secrets SecretStore) *Service {
	return &Service{secrets: secrets}
}

type Status struct {
	Unlocked bool
}

func (s *Service) Status(ctx context.Context) Status {
	if s == nil || s.secrets == nil {
		return Status{}
	}
	return Status{Unlocked: s.secrets.Unlocked()}
}

type UnlockInput struct {
	Passphrase string
}

type UnlockResult struct {
	Unlocked bool
}

func (s *Service) Unlock(ctx context.Context, input UnlockInput) (UnlockResult, error) {
	if s == nil || s.secrets == nil {
		return UnlockResult{}, nil
	}
	passphrase := strings.TrimSpace(input.Passphrase)
	if passphrase == "" {
		return UnlockResult{Unlocked: s.secrets.Unlocked()}, ErrPassphraseRequired
	}
	if err := s.secrets.Unlock(ctx, passphrase); err != nil {
		return UnlockResult{Unlocked: s.secrets.Unlocked()}, err
	}
	return UnlockResult{Unlocked: s.secrets.Unlocked()}, nil
}
