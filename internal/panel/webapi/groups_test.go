package webapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

func TestGroupPolicyAndMemberAPI(t *testing.T) {
	c, svc, pw := newServer(t)
	ctx := context.Background()
	g, err := svc.CreateGroup(ctx, service.ActorCLI, "Trial", "")
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/groups/" + itoa(g.ID)
	if w := c.do("GET", path+"/users", "", false); w.Code != 401 {
		t.Fatalf("auth: %d", w.Code)
	}
	c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true)
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if _, err := svc.CreateUser(ctx, service.ActorCLI, service.CreateUserInput{Username: name}); err != nil {
			t.Fatal(err)
		}
	}
	if w := c.do("PATCH", path, `{"name":"Trial","defaults":{"expiry":"period","days":10,"traffic":"limit","trafficLimit":1024}}`, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := c.do("PATCH", path, `{"name":"Trial renamed"}`, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	saved, _ := svc.Group(ctx, g.ID)
	if saved.Defaults == nil || saved.Defaults.Days != 10 {
		t.Fatal("absent defaults cleared policy")
	}
	if w := c.do("POST", path+"/users", `{"action":"add","ids":[1,2],"applyDefaults":true}`, false); w.Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	if w := c.do("POST", path+"/users", `{"action":"add","ids":[1,2],"applyDefaults":true}`, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var page struct {
		Users []User
		Total int
	}
	w := c.do("GET", path+"/users?limit=1&offset=1", "", false)
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Users) != 1 || page.Users[0].Username != "beta" || *page.Users[0].TrafficLimit != 1024 {
		t.Fatalf("members: %s", w.Body.String())
	}
	w = c.do("GET", path+"/users?members=false&search=gam", "", false)
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Users) != 1 || page.Users[0].Username != "gamma" {
		t.Fatalf("nonmembers: %s", w.Body.String())
	}
	w = c.do("POST", path+"/users", `{"action":"create","username":"new-member"}`, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"trafficLimit":1024`) {
		t.Fatalf("create: %s", w.Body.String())
	}
	if w := c.do("POST", path+"/users", `{"action":"remove","ids":[1,2]}`, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := c.do("POST", path+"/users", `{"action":"delete","ids":[1]}`, true); w.Code != 400 {
		t.Fatalf("bad action: %d", w.Code)
	}
	if w := c.do("PATCH", path, `{"name":"Trial","defaults":null}`, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	saved, _ = svc.Group(ctx, g.ID)
	if saved.Defaults != nil {
		t.Fatal("explicit null not saved")
	}
}
