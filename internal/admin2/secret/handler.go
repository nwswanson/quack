package secret

import (
	"errors"
	"log/slog"
	"net/http"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	status := h.svc.Status(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := SecretPage(SecretPageVM{
		Username: AdminUserFromContext(r.Context()).Username,
		Unlocked: status.Unlocked,
	}).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "render admin2 secret page failed", "error", err)
	}
}

func (h *Handler) Unlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	form, err := DecodeUnlockForm(r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = UnlockPanel(UnlockPanelVM{
			Error: "Invalid form submission.",
		}).Render(r.Context(), w)
		return
	}

	result, err := h.svc.Unlock(r.Context(), form.ToInput())
	if err != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
		msg := "Could not unlock the secret store."
		if errors.Is(err, ErrPassphraseRequired) {
			msg = "Passphrase is required."
		}
		_ = UnlockPanel(UnlockPanelVM{
			Unlocked: result.Unlocked,
			Error:    msg,
		}).Render(r.Context(), w)
		return
	}

	_ = UnlockPanel(UnlockPanelVM{
		Unlocked: result.Unlocked,
		Success:  "Secret store unlocked.",
	}).Render(r.Context(), w)
}
