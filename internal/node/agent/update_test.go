package agent

import (
	"context"
	"io"
	"log/slog"
	"testing"

	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"google.golang.org/protobuf/proto"
)

func TestUpdateCommandPreservesXrayFieldPresence(t *testing.T) {
	latest, pin := "", "v26.9.30"
	for _, want := range []*string{nil, &latest, &pin} {
		wire, err := proto.Marshal(&nodev1.Command{Action: nodev1.Command_ACTION_UPDATE, Release: "edge-test", Ref: "test", XrayRelease: want})
		if err != nil {
			t.Fatal(err)
		}
		var command nodev1.Command
		if err := proto.Unmarshal(wire, &command); err != nil {
			t.Fatal(err)
		}
		called := false
		a := &Agent{log: slog.New(slog.NewTextHandler(io.Discard, nil)), cfg: Config{Update: func(_ context.Context, release, ref string, got *string) error {
			called = true
			if release != "edge-test" || ref != "test" || (got == nil) != (want == nil) || (want != nil && *got != *want) {
				t.Fatalf("command lost the Xray selection: %+v, %v", &command, got)
			}
			return nil
		}}}
		result, _ := a.command(t.Context(), &command)
		if !called || result.Error != "" {
			t.Fatalf("update failed: %+v", result)
		}
	}
}
