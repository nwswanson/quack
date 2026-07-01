package secret

import "context"

type AdminUser struct {
	Username string
}

type adminUserContextKey struct{}

func ContextWithAdminUser(ctx context.Context, user AdminUser) context.Context {
	return context.WithValue(ctx, adminUserContextKey{}, user)
}

func AdminUserFromContext(ctx context.Context) AdminUser {
	user, _ := ctx.Value(adminUserContextKey{}).(AdminUser)
	return user
}
