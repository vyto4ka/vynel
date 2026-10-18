package agent

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
)

// CapChunks is the capability of agents that put a snapshot sent in parts back together
// (Snapshot.part, Snapshot.parts): one gRPC message has a size limit, and a node with many
// users would never get its config otherwise.
const CapChunks = "chunks"

// snapshotParts collects the parts of one snapshot. It lives for one session: a reconnect
// starts over, and the panel sends the snapshot again.
type snapshotParts struct {
	snap *nodev1.Snapshot
	next int32
}

// add takes one part. It returns the whole snapshot once the last part is in, nil while parts
// are still missing, and an error for a part that does not follow the previous one (the panel
// then sends the snapshot again).
func (p *snapshotParts) add(s *nodev1.Snapshot) (*nodev1.Snapshot, error) {
	if s.Parts <= 0 {
		p.snap = nil
		return s, nil
	}
	if s.Part == 0 {
		p.snap = proto.Clone(s).(*nodev1.Snapshot)
		p.snap.Part, p.snap.Parts = 0, 0
		p.next = 1
	} else {
		if p.snap == nil || s.Revision != p.snap.Revision || s.Hash != p.snap.Hash || s.Part != p.next {
			p.snap = nil
			return nil, fmt.Errorf("snapshot part %d of %d (revision %d) came out of order", s.Part+1, s.Parts, s.Revision)
		}
		for _, in := range s.Inbounds {
			// An inbound whose users did not fit continues at the start of the next part.
			if n := len(p.snap.Inbounds); n > 0 && p.snap.Inbounds[n-1].Tag == in.Tag {
				p.snap.Inbounds[n-1].Users = append(p.snap.Inbounds[n-1].Users, in.Users...)
				continue
			}
			p.snap.Inbounds = append(p.snap.Inbounds, in)
		}
		p.next++
	}
	if s.Part < s.Parts-1 {
		return nil, nil
	}
	whole := p.snap
	p.snap = nil
	return whole, nil
}
