package modules

import (
	"context"
	"testing"

	"quack/internal/domain"

	"go.starlark.net/starlark"
)

func TestSecretExistsChecksSpecificSecret(t *testing.T) {
	store := &fakeSecretStore{
		unlocked: true,
		values: map[string]string{
			"site\x00example\x00hello": "world",
		},
	}
	globals, err := starlark.ExecFile(&starlark.Thread{Name: "test"}, "test.star", `
store_unlocked = secret.unlocked()
hello_exists = secret.exists("site", "hello")
missing_exists = secret.exists("site", "hello2")
hello = secret.get("site", "hello")
`, starlark.StringDict{
		"secret": NewSecretModule(context.Background(), "example", store),
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	assertBool(t, globals["store_unlocked"], true)
	assertBool(t, globals["hello_exists"], true)
	assertBool(t, globals["missing_exists"], false)
	if got := string(globals["hello"].(starlark.String)); got != "world" {
		t.Fatalf("hello = %q, want world", got)
	}
}

func TestSecretExistsIsFalseWhenStoreLocked(t *testing.T) {
	store := &fakeSecretStore{values: map[string]string{
		"site\x00example\x00hello": "world",
	}}
	globals, err := starlark.ExecFile(&starlark.Thread{Name: "test"}, "test.star", `
store_unlocked = secret.unlocked()
hello_exists = secret.exists("site", "hello")
`, starlark.StringDict{
		"secret": NewSecretModule(context.Background(), "example", store),
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	assertBool(t, globals["store_unlocked"], false)
	assertBool(t, globals["hello_exists"], false)
}

type fakeSecretStore struct {
	unlocked bool
	values   map[string]string
}

func (s *fakeSecretStore) Available(ctx context.Context, site string, scope domain.SecretScope, name string) (bool, error) {
	if !s.unlocked {
		return false, nil
	}
	_, ok := s.values[string(scope)+"\x00"+site+"\x00"+name]
	return ok, nil
}

func (s *fakeSecretStore) Get(ctx context.Context, site string, scope domain.SecretScope, name string) (string, error) {
	return s.values[string(scope)+"\x00"+site+"\x00"+name], nil
}

func (s *fakeSecretStore) Unlocked() bool {
	return s.unlocked
}

func assertBool(t *testing.T, value starlark.Value, want bool) {
	t.Helper()
	got, ok := value.(starlark.Bool)
	if !ok {
		t.Fatalf("value type = %T, want starlark.Bool", value)
	}
	if bool(got) != want {
		t.Fatalf("value = %v, want %v", got, want)
	}
}
