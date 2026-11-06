package webapi

import (
	"net/http"

	"github.com/vyto4ka/vynel/internal/i18n"
)

const maxBulkUsers = 500

type bulkUserFailure struct {
	ID    int64  `json:"id"`
	Error string `json:"error"`
}

// Each user uses the same service operation, audit entry and reconciliation as an individual
// action. One missing user does not prevent processing the rest; the caller gets exact outcomes.
func (s *Server) bulkUsers(r *http.Request) (any, error) {
	var in struct {
		IDs    []int64 `json:"ids"`
		Action string  `json:"action"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if len(in.IDs) == 0 || len(in.IDs) > maxBulkUsers {
		return nil, badRequest("выберите от 1 до 500 пользователей")
	}
	switch in.Action {
	case "enable", "disable", "reset", "delete":
	default:
		return nil, badRequest("действие: enable, disable, reset или delete")
	}
	// Validate the entire request before changing anyone, and apply each ID at most once.
	ids, seen := make([]int64, 0, len(in.IDs)), map[int64]bool{}
	for _, id := range in.IDs {
		if id <= 0 {
			return nil, badRequest("ID пользователя должен быть положительным числом")
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	ctx, a := r.Context(), actor(r)
	succeeded := []int64{}
	failed := []bulkUserFailure{}
	for _, id := range ids {
		err := ctx.Err()
		if err == nil {
			switch in.Action {
			case "enable", "disable":
				_, err = s.svc.SetUserEnabled(ctx, a, id, in.Action == "enable")
			case "reset":
				_, err = s.svc.ResetUserTraffic(ctx, a, id)
			case "delete":
				err = s.svc.DeleteUser(ctx, a, id)
			}
		}
		if err != nil {
			failed = append(failed, bulkUserFailure{ID: id, Error: i18n.Error(i18n.FromRequest(r), err)})
		} else {
			succeeded = append(succeeded, id)
		}
	}
	return map[string]any{"succeeded": succeeded, "failed": failed}, nil
}
