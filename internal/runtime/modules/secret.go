package modules

import (
	"context"
	"fmt"

	"quack/internal/domain"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

type SecretGetter interface {
	Get(ctx context.Context, site string, scope domain.SecretScope, name string) (string, error)
	Unlocked() bool
}

type secretModule struct {
	ctx   context.Context
	site  string
	store SecretGetter
}

func NewSecretModule(ctx context.Context, site string, store SecretGetter) *starlarkstruct.Module {
	m := &secretModule{ctx: ctx, site: site, store: store}
	return &starlarkstruct.Module{
		Name: "secret",
		Members: starlark.StringDict{
			"get":      starlark.NewBuiltin("secret.get", m.get),
			"unlocked": starlark.NewBuiltin("secret.unlocked", m.unlocked),
		},
	}
}

func (m *secretModule) get(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var scope, name string
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "scope", &scope, "name", &name); err != nil {
		return nil, err
	}
	if m.store == nil {
		return nil, fmt.Errorf("secrets are not configured")
	}
	value, err := m.store.Get(m.ctx, m.site, domain.SecretScope(scope), name)
	if err != nil {
		return nil, err
	}
	return starlark.String(value), nil
}

func (m *secretModule) unlocked(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs); err != nil {
		return nil, err
	}
	return starlark.Bool(m.store != nil && m.store.Unlocked()), nil
}
