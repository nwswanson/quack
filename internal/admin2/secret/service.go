package secret

import (
	"context"
	"errors"
	"strings"
)

var ErrPassphraseRequired = errors.New("passphrase required")

type SecretStore interface {
	Status(ctx context.Context) (hasKey bool, unlocked bool, err error)
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
	Configured bool
	Unlocked   bool
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	if s == nil || s.secrets == nil {
		return Status{}, nil
	}
	configured, unlocked, err := s.secrets.Status(ctx)
	if err != nil {
		return Status{}, err
	}
	return Status{Configured: configured, Unlocked: unlocked}, nil
}

type UnlockInput struct {
	Passphrase string
}

type UnlockResult struct {
	Configured bool
	Unlocked   bool
}

func (s *Service) Unlock(ctx context.Context, input UnlockInput) (UnlockResult, error) {
	if s == nil || s.secrets == nil {
		return UnlockResult{}, nil
	}
	passphrase := strings.TrimSpace(input.Passphrase)
	if passphrase == "" {
		status, err := s.Status(ctx)
		return UnlockResult{Configured: status.Configured, Unlocked: status.Unlocked}, errors.Join(ErrPassphraseRequired, err)
	}
	if err := s.secrets.Unlock(ctx, passphrase); err != nil {
		status, statusErr := s.Status(ctx)
		return UnlockResult{Configured: status.Configured, Unlocked: status.Unlocked}, errors.Join(err, statusErr)
	}
	status, err := s.Status(ctx)
	return UnlockResult{Configured: status.Configured, Unlocked: status.Unlocked}, err
}
