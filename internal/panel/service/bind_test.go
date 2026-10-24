package service

import (
	"errors"
	"testing"
)

// Ten wrong codes burn the current bind code, and a new code binds again: guessing cannot lock
// the admins out.
func TestBindCodeBurnsAndRecovers(t *testing.T) {
	f := newFixture(t)
	code := must(f.s.NewBindCode(f.ctx, ActorCLI))
	var err error
	for i := 0; i < MaxBindFailures; i++ {
		var ok bool
		ok, err = f.s.BindBotAdmin(f.ctx, "00000000", BotAdmin{ID: 99})
		if ok {
			t.Fatal("a wrong code bound")
		}
	}
	if !errors.Is(err, ErrBindCodeBurned) {
		t.Fatalf("the tenth wrong code: %v", err)
	}
	if ok, _ := f.s.BindBotAdmin(f.ctx, code, BotAdmin{ID: 42}); ok {
		t.Fatal("a burnt code still binds")
	}
	fresh := must(f.s.NewBindCode(f.ctx, ActorCLI))
	if ok, _ := f.s.BindBotAdmin(f.ctx, "11111111", BotAdmin{ID: 99}); ok {
		t.Fatal("a wrong code bound")
	}
	if ok, err := f.s.BindBotAdmin(f.ctx, fresh, BotAdmin{ID: 42}); !ok || err != nil {
		t.Fatalf("a new code does not bind: %v %v", ok, err)
	}
	if !f.s.IsBotAdmin(f.ctx, 42) {
		t.Fatal("not bound")
	}
}
