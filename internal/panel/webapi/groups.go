package webapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

type groupInput struct {
	Name, Description string
	Defaults          json.RawMessage `json:"defaults"`
}

func (in groupInput) serviceInput() (service.GroupInput, error) {
	out := service.GroupInput{Name: in.Name, Description: in.Description, SetDefaults: in.Defaults != nil}
	if in.Defaults != nil {
		if err := json.Unmarshal(in.Defaults, &out.Defaults); err != nil {
			return out, badRequest("invalid group defaults")
		}
	}
	return out, nil
}

// Membership search is paginated on the server, including nonmembers. Other group
// memberships are included so the administrator can see what access is retained.
func (s *Server) groupUsers(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	if _, err := s.svc.Group(ctx, id); err != nil {
		return nil, err
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	f := store.UserFilter{GroupID: &id, ExcludeGroup: q.Get("members") == "false", Search: q.Get("search"), Sort: "username", Limit: min(limit, 500), Offset: max(offset, 0)}
	us, err := s.svc.Users(ctx, f)
	if err != nil {
		return nil, err
	}
	groups, err := s.svc.UsersGroups(ctx, us)
	if err != nil {
		return nil, err
	}
	out := make([]User, 0, len(us))
	for _, u := range us {
		out = append(out, userDTO(u, groups[u.ID]))
	}
	total, err := s.svc.CountUsers(ctx, f)
	return map[string]any{"users": out, "total": total}, err
}

func (s *Server) changeGroupUsers(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var in struct {
		Action        string  `json:"action"`
		IDs           []int64 `json:"ids"`
		ApplyDefaults bool    `json:"applyDefaults"`
		Username      string  `json:"username"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	switch in.Action {
	case "create":
		u, err := s.svc.CreateUser(r.Context(), actor(r), service.CreateUserInput{Username: in.Username, GroupDefaultsID: id})
		if err != nil {
			return nil, err
		}
		return userDTO(u, []int64{id}), nil
	case "add", "remove":
		return s.svc.ChangeGroupMembers(r.Context(), actor(r), id, in.IDs, in.Action == "add", in.ApplyDefaults)
	default:
		return nil, badRequest("action: add, remove or create")
	}
}
