package server

func Can(user AdminUser, action string) bool {
	if user.IsAdmin() {
		return true
	}
	switch action {
	case "sites.upload", "sites.delete":
		return user.ID > 0
	default:
		return false
	}
}
