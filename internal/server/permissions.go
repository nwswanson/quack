package server

import "quack/internal/access"

func Can(user AdminUser, action string) bool {
	return access.Can(user, action)
}
