package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// Group is an internal squad: a set of access rules.
type Group struct {
	ID          int64
	Name        string
	Description string
	Sort        int64
	CreatedAt   int64
}

// Access kinds (docs/INBOUNDS.md §1.5).
const (
	AccessNodeInbound = "node_inbound"
	AccessProfile     = "profile"
	AccessNode        = "node"
)

// AccessRule grants a group access to inbounds.
type AccessRule struct {
	ID      int64
	GroupID int64
	Kind    string
	RefID   int64
}

// CreateGroup inserts a group.
func CreateGroup(ctx context.Context, q DBTX, g *Group) error {
	g.CreatedAt = unix()
	res, err := q.ExecContext(ctx, `INSERT INTO groups (name, description, sort, created_at) VALUES (?, ?, ?, ?)`, g.Name, g.Description, g.Sort, g.CreatedAt)
	if err != nil {
		return mapErr(err)
	}
	g.ID, err = res.LastInsertId()
	return err
}

// UpdateGroup saves a group.
func UpdateGroup(ctx context.Context, q DBTX, g *Group) error {
	return execOne(ctx, q, `UPDATE groups SET name=?, description=?, sort=? WHERE id=?`, g.Name, g.Description, g.Sort, g.ID)
}

// GetGroup loads a group by id.
func GetGroup(ctx context.Context, q DBTX, id int64) (*Group, error) {
	g := &Group{}
	err := q.QueryRowContext(ctx, `SELECT id, name, description, sort, created_at FROM groups WHERE id=?`, id).
		Scan(&g.ID, &g.Name, &g.Description, &g.Sort, &g.CreatedAt)
	return g, mapErr(err)
}

// GetGroupByName loads a group by name.
func GetGroupByName(ctx context.Context, q DBTX, name string) (*Group, error) {
	g := &Group{}
	err := q.QueryRowContext(ctx, `SELECT id, name, description, sort, created_at FROM groups WHERE name=?`, name).
		Scan(&g.ID, &g.Name, &g.Description, &g.Sort, &g.CreatedAt)
	return g, mapErr(err)
}

// ListGroups returns all groups.
func ListGroups(ctx context.Context, q DBTX) ([]*Group, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, name, description, sort, created_at FROM groups ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Group
	for rows.Next() {
		g := &Group{}
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.Sort, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// DeleteGroup removes a group (memberships and rules cascade).
func DeleteGroup(ctx context.Context, q DBTX, id int64) error {
	return execOne(ctx, q, `DELETE FROM groups WHERE id=?`, id)
}

// AddAccessRule adds a rule; adding an existing rule is a no-op.
func AddAccessRule(ctx context.Context, q DBTX, r *AccessRule) error {
	err := q.QueryRowContext(ctx, `INSERT INTO group_access (group_id, kind, ref_id) VALUES (?, ?, ?)
		ON CONFLICT (group_id, kind, ref_id) DO UPDATE SET kind=excluded.kind RETURNING id`, r.GroupID, r.Kind, r.RefID).Scan(&r.ID)
	return mapErr(err)
}

// RemoveAccessRule deletes a rule.
func RemoveAccessRule(ctx context.Context, q DBTX, groupID int64, kind string, refID int64) error {
	return execOne(ctx, q, `DELETE FROM group_access WHERE group_id=? AND kind=? AND ref_id=?`, groupID, kind, refID)
}

// ListAccessRules returns rules of one group (0 = all groups).
func ListAccessRules(ctx context.Context, q DBTX, groupID int64) ([]*AccessRule, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, group_id, kind, ref_id FROM group_access WHERE (?=0 OR group_id=?) ORDER BY id`, groupID, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AccessRule
	for rows.Next() {
		r := &AccessRule{}
		if err := rows.Scan(&r.ID, &r.GroupID, &r.Kind, &r.RefID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteAccessRulesByRef removes rules pointing at a deleted object.
func DeleteAccessRulesByRef(ctx context.Context, q DBTX, kind string, refID int64) error {
	_, err := q.ExecContext(ctx, `DELETE FROM group_access WHERE kind=? AND ref_id=?`, kind, refID)
	return err
}

// UserTemplate is a preset for new users.
type UserTemplate struct {
	ID                int64
	Name              string
	IsDefault         bool
	ExpireMonths      int
	ExpireDays        int
	TrafficLimitBytes *int64
	ResetStrategy     string
	HWIDLimit         *int64
	ClientType        string
	GroupIDs          []int64
	Note              string
	CreatedAt         int64
}

const userTemplateCols = `id, name, is_default, expire_months, expire_days, traffic_limit_bytes, reset_strategy, hwid_limit, client_type, group_ids, note, created_at`

func scanUserTemplate(sc interface{ Scan(...any) error }) (*UserTemplate, error) {
	t := &UserTemplate{}
	var limit, hwid sql.NullInt64
	var groups string
	if err := sc.Scan(&t.ID, &t.Name, &t.IsDefault, &t.ExpireMonths, &t.ExpireDays, &limit, &t.ResetStrategy, &hwid, &t.ClientType, &groups, &t.Note, &t.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	t.TrafficLimitBytes, t.HWIDLimit = ptrInt(limit), ptrInt(hwid)
	_ = json.Unmarshal([]byte(groups), &t.GroupIDs)
	return t, nil
}

func clearDefaultTemplate(ctx context.Context, q DBTX, t *UserTemplate) error {
	if !t.IsDefault {
		return nil
	}
	_, err := q.ExecContext(ctx, `UPDATE user_templates SET is_default=0 WHERE id != ?`, t.ID)
	return err
}

// CreateUserTemplate inserts a template; a new default unsets the previous one.
func CreateUserTemplate(ctx context.Context, q DBTX, t *UserTemplate) error {
	t.CreatedAt = unix()
	if t.GroupIDs == nil {
		t.GroupIDs = []int64{}
	}
	res, err := q.ExecContext(ctx, `INSERT INTO user_templates (name, is_default, expire_months, expire_days, traffic_limit_bytes, reset_strategy, hwid_limit, client_type, group_ids, note, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, t.Name, t.IsDefault, t.ExpireMonths, t.ExpireDays, nullInt(t.TrafficLimitBytes), t.ResetStrategy,
		nullInt(t.HWIDLimit), t.ClientType, toJSON(t.GroupIDs), t.Note, t.CreatedAt)
	if err != nil {
		return mapErr(err)
	}
	if t.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	return clearDefaultTemplate(ctx, q, t)
}

// UpdateUserTemplate saves a template.
func UpdateUserTemplate(ctx context.Context, q DBTX, t *UserTemplate) error {
	if t.GroupIDs == nil {
		t.GroupIDs = []int64{}
	}
	if err := execOne(ctx, q, `UPDATE user_templates SET name=?, is_default=?, expire_months=?, expire_days=?, traffic_limit_bytes=?, reset_strategy=?, hwid_limit=?, client_type=?, group_ids=?, note=?
		WHERE id=?`, t.Name, t.IsDefault, t.ExpireMonths, t.ExpireDays, nullInt(t.TrafficLimitBytes), t.ResetStrategy, nullInt(t.HWIDLimit),
		t.ClientType, toJSON(t.GroupIDs), t.Note, t.ID); err != nil {
		return err
	}
	return clearDefaultTemplate(ctx, q, t)
}

// GetUserTemplate loads a template by id; id 0 returns the default template.
func GetUserTemplate(ctx context.Context, q DBTX, id int64) (*UserTemplate, error) {
	if id == 0 {
		return scanUserTemplate(q.QueryRowContext(ctx, `SELECT `+userTemplateCols+` FROM user_templates WHERE is_default=1`))
	}
	return scanUserTemplate(q.QueryRowContext(ctx, `SELECT `+userTemplateCols+` FROM user_templates WHERE id=?`, id))
}

// GetUserTemplateByName loads a template by name.
func GetUserTemplateByName(ctx context.Context, q DBTX, name string) (*UserTemplate, error) {
	return scanUserTemplate(q.QueryRowContext(ctx, `SELECT `+userTemplateCols+` FROM user_templates WHERE name=?`, name))
}

// ListUserTemplates returns all templates.
func ListUserTemplates(ctx context.Context, q DBTX) ([]*UserTemplate, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+userTemplateCols+` FROM user_templates ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*UserTemplate
	for rows.Next() {
		t, err := scanUserTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteUserTemplate removes a template.
func DeleteUserTemplate(ctx context.Context, q DBTX, id int64) error {
	return execOne(ctx, q, `DELETE FROM user_templates WHERE id=?`, id)
}

// User statuses (docs/ARCHITECTURE.md §7.2).
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusLimited  = "limited"
	StatusExpired  = "expired"
)

// User is a VPN user.
type User struct {
	ID                int64
	Username          string
	UUID              string
	SubToken          string
	Disabled          bool
	Status            string
	ExpireAt          *int64
	TrafficLimitBytes *int64
	TrafficUsedBytes  int64
	LifetimeUsedBytes int64
	ResetStrategy     string
	LastResetAt       *int64
	HWIDLimit         *int64
	ClientType        string
	TemplateID        *int64
	TelegramID        *int64
	ExternalID        *string
	Note              string
	OnlineAt          *int64
	CreatedBy         string
	CreatedAt         int64
	UpdatedAt         int64
	SubLastAt         *int64
	SubLastUA         string
}

const userCols = `id, username, uuid, sub_token, disabled, status, expire_at, traffic_limit_bytes, traffic_used_bytes, lifetime_used_bytes,
	reset_strategy, last_reset_at, hwid_limit, client_type, template_id, telegram_id, external_id, note, online_at, created_by, created_at, updated_at,
	sub_last_at, sub_last_ua`

func scanUser(sc interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	var expire, limit, reset, hwid, tpl, tg, online, subAt sql.NullInt64
	var ext sql.NullString
	if err := sc.Scan(&u.ID, &u.Username, &u.UUID, &u.SubToken, &u.Disabled, &u.Status, &expire, &limit, &u.TrafficUsedBytes, &u.LifetimeUsedBytes,
		&u.ResetStrategy, &reset, &hwid, &u.ClientType, &tpl, &tg, &ext, &u.Note, &online, &u.CreatedBy, &u.CreatedAt, &u.UpdatedAt,
		&subAt, &u.SubLastUA); err != nil {
		return nil, mapErr(err)
	}
	u.SubLastAt = ptrInt(subAt)
	u.ExpireAt, u.TrafficLimitBytes, u.LastResetAt, u.HWIDLimit = ptrInt(expire), ptrInt(limit), ptrInt(reset), ptrInt(hwid)
	u.TemplateID, u.TelegramID, u.OnlineAt, u.ExternalID = ptrInt(tpl), ptrInt(tg), ptrInt(online), ptrStr(ext)
	return u, nil
}

// CreateUser inserts a user.
func CreateUser(ctx context.Context, q DBTX, u *User) error {
	now := unix()
	u.CreatedAt, u.UpdatedAt = now, now
	res, err := q.ExecContext(ctx, `INSERT INTO users (username, uuid, sub_token, disabled, status, expire_at, traffic_limit_bytes, reset_strategy, hwid_limit,
		client_type, template_id, telegram_id, external_id, note, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.Username, u.UUID, u.SubToken, u.Disabled, u.Status, nullInt(u.ExpireAt), nullInt(u.TrafficLimitBytes), u.ResetStrategy, nullInt(u.HWIDLimit),
		u.ClientType, nullInt(u.TemplateID), nullInt(u.TelegramID), nullStr(u.ExternalID), u.Note, u.CreatedBy, now, now)
	if err != nil {
		return mapErr(err)
	}
	u.ID, err = res.LastInsertId()
	return err
}

// UpdateUser saves all editable user fields (not traffic counters).
func UpdateUser(ctx context.Context, q DBTX, u *User) error {
	u.UpdatedAt = unix()
	return execOne(ctx, q, `UPDATE users SET username=?, uuid=?, sub_token=?, disabled=?, status=?, expire_at=?, traffic_limit_bytes=?, traffic_used_bytes=?,
		reset_strategy=?, last_reset_at=?, hwid_limit=?, client_type=?, telegram_id=?, external_id=?, note=?, updated_at=? WHERE id=?`,
		u.Username, u.UUID, u.SubToken, u.Disabled, u.Status, nullInt(u.ExpireAt), nullInt(u.TrafficLimitBytes), u.TrafficUsedBytes,
		u.ResetStrategy, nullInt(u.LastResetAt), nullInt(u.HWIDLimit), u.ClientType, nullInt(u.TelegramID), nullStr(u.ExternalID), u.Note, u.UpdatedAt, u.ID)
}

// SetUserStatus changes only the status.
func SetUserStatus(ctx context.Context, q DBTX, id int64, status string) error {
	return execOne(ctx, q, `UPDATE users SET status=?, updated_at=? WHERE id=?`, status, unix(), id)
}

// GetUser loads a user by id.
func GetUser(ctx context.Context, q DBTX, id int64) (*User, error) {
	return scanUser(q.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id=?`, id))
}

// GetUserBySubToken loads a user by subscription token.
func GetUserBySubToken(ctx context.Context, q DBTX, token string) (*User, error) {
	return scanUser(q.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE sub_token=?`, token))
}

// TouchSubscription records the last subscription fetch.
func TouchSubscription(ctx context.Context, q DBTX, id, ts int64, ua string) error {
	_, err := q.ExecContext(ctx, `UPDATE users SET sub_last_at=?, sub_last_ua=? WHERE id=?`, ts, ua, id)
	return err
}

// GetUserByUsername loads a user by username.
func GetUserByUsername(ctx context.Context, q DBTX, username string) (*User, error) {
	return scanUser(q.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username=?`, username))
}

// UserFilter narrows ListUsers.
type UserFilter struct {
	Status string // '' = any
	Search string // substring of username or note
	Limit  int
	Offset int
}

// ListUsers returns users matching the filter.
func ListUsers(ctx context.Context, q DBTX, f UserFilter) ([]*User, error) {
	where, args := []string{"1=1"}, []any{}
	if f.Status != "" {
		where, args = append(where, "status=?"), append(args, f.Status)
	}
	if f.Search != "" {
		where = append(where, "(username LIKE ? OR note LIKE ?)")
		like := "%" + f.Search + "%"
		args = append(args, like, like)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = -1
	}
	args = append(args, limit, f.Offset)
	rows, err := q.QueryContext(ctx, `SELECT `+userCols+` FROM users WHERE `+strings.Join(where, " AND ")+` ORDER BY id LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// DeleteUser removes a user.
func DeleteUser(ctx context.Context, q DBTX, id int64) error {
	return execOne(ctx, q, `DELETE FROM users WHERE id=?`, id)
}

// SetUserGroups replaces a user's group memberships.
func SetUserGroups(ctx context.Context, q DBTX, userID int64, groupIDs []int64) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM user_groups WHERE user_id=?`, userID); err != nil {
		return err
	}
	for _, g := range groupIDs {
		if _, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO user_groups (user_id, group_id) VALUES (?, ?)`, userID, g); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

// UserGroupIDs returns a user's groups.
func UserGroupIDs(ctx context.Context, q DBTX, userID int64) ([]int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT group_id FROM user_groups WHERE user_id=? ORDER BY group_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ActiveMember is an active user with one of its groups (used to build desired node state).
type ActiveMember struct {
	UserID  int64
	UUID    string
	GroupID int64
}

// ListActiveMembers returns (active user, group) pairs.
func ListActiveMembers(ctx context.Context, q DBTX) ([]ActiveMember, error) {
	rows, err := q.QueryContext(ctx, `SELECT u.id, u.uuid, ug.group_id FROM users u JOIN user_groups ug ON ug.user_id = u.id
		WHERE u.status='active' ORDER BY u.id, ug.group_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ActiveMember
	for rows.Next() {
		var m ActiveMember
		if err := rows.Scan(&m.UserID, &m.UUID, &m.GroupID); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GroupMemberCounts returns group id -> number of users in it.
func GroupMemberCounts(ctx context.Context, q DBTX) (map[int64]int, error) {
	rows, err := q.QueryContext(ctx, `SELECT group_id, count(*) FROM user_groups GROUP BY group_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var g int64
		var n int
		if err := rows.Scan(&g, &n); err != nil {
			return nil, err
		}
		out[g] = n
	}
	return out, rows.Err()
}
