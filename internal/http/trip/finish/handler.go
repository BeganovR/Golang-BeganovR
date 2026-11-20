package finish

import (
	"context"
	"net/http"
	"time"

	"github.com/BeganovR/Golang-BeganovR/api"
	"github.com/BeganovR/Golang-BeganovR/internal/http/response"
)

type Handler struct {
	usecase usecase
	timeout time.Duration
}

func New(uc usecase, timeout time.Duration) *Handler {
	return &Handler{usecase: uc, timeout: timeout}
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request, id api.TripId) {
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()
	t, err := h.usecase.Execute(ctx, id)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.JSON(w, r, http.StatusOK, response.Trip(t))
}
