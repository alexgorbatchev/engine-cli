package transfer

import (
	"context"

	acoustic "github.com/alexgorbatchev/engine-cli/internal/fingerprint"
)

type AudioMatch struct {
	SourceID      int64           `json:"source_id"`
	DestinationID int64           `json:"destination_id"`
	Method        string          `json:"method"`
	Evidence      *acoustic.Match `json:"evidence,omitempty"`
	Reused        bool            `json:"reused"`
}

func nativeTrackKey(e *entity) string {
	return "track:" + identity(str(e.Values["originDatabaseUuid"]), number(e.Values["originTrackId"]))
}

// Audio associations reuse the destination entry, audio, metadata and timing.
// They never transfer cue/grid data between independently imported recordings.
func (p *plan) matchAudio(ctx context.Context) error {
	p.reused = map[string]bool{}
	p.report.AudioMatches = []AudioMatch{}
	sourceIndex, err := acoustic.Load(p.source.Path)
	if err != nil {
		return err
	}
	destIndex, err := acoustic.Load(p.destination.Path)
	if err != nil {
		return err
	}
	type link struct {
		source, destination string
		match               AudioMatch
	}
	var links []link
	sourceCount, destCount := map[string]int{}, map[string]int{}
	for _, sk := range orderedKeys(p.source.Entities) {
		s := p.source.Entities[sk]
		if s.Kind != "track" || !p.selected[sk] {
			continue
		}
		if d := p.destination.Entities[sk]; d != nil {
			if nativeTrackKey(s) != nativeTrackKey(d) {
				m, ok, err := compareAudio(ctx, s, d, sourceIndex, destIndex)
				if err != nil {
					return err
				}
				if !ok {
					p.conflict(Change{Entity: "track", ID: sk, SourceID: s.ID, DestinationID: d.ID}, "sync_audio_unverified", "linked audio changed or lacks current fingerprints; scan and resolve before syncing")
				} else {
					m.Reused = true
					p.reused[sk] = true
					p.report.AudioMatches = append(p.report.AudioMatches, m)
				}
			}
			continue
		}
		if s.Hash == "" {
			continue
		}
		for _, dk := range orderedKeys(p.destination.Entities) {
			d := p.destination.Entities[dk]
			if d.Kind != "track" || d.Hash == "" {
				continue
			}
			m, ok, err := compareAudio(ctx, s, d, sourceIndex, destIndex)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			links = append(links, link{sk, dk, m})
			sourceCount[sk]++
			destCount[dk]++
		}
	}
	for _, link := range links {
		m := link.match
		if sourceCount[link.source] != 1 || destCount[link.destination] != 1 || (p.selected[link.destination] && p.source.Entities[link.destination] != nil) {
			p.conflict(Change{Entity: "track", ID: link.source, SourceID: m.SourceID, DestinationID: m.DestinationID}, "sync_audio_ambiguous", "multiple audio matches; resolve duplicate candidates before syncing")
		} else {
			p.remapAudio(link.destination, link.source)
			m.Reused = true
			p.reused[link.source] = true
			p.report.Changes = append(p.report.Changes, Change{Entity: "track", ID: link.source, Action: "reuse", SourceID: m.SourceID, DestinationID: m.DestinationID, Code: "sync_audio_reused", Reason: "reuse destination audio, metadata, cues and grid"})
		}
		p.report.AudioMatches = append(p.report.AudioMatches, m)
	}
	return nil
}

func compareAudio(ctx context.Context, s, d *entity, sourceIndex, destIndex acoustic.Index) (AudioMatch, bool, error) {
	m := AudioMatch{SourceID: s.ID, DestinationID: d.ID}
	if s.Hash == "" || d.Hash == "" {
		return m, false, nil
	}
	if s.Hash == d.Hash {
		m.Method = "identical_file"
		return m, true, nil
	}
	a, aOK := sourceIndex[s.Hash]
	b, bOK := destIndex[d.Hash]
	if !aOK || !bOK {
		return m, false, nil
	}
	evidence, err := acoustic.Compare(ctx, a, b)
	if err != nil {
		return m, false, err
	}
	m.Method = "acoustic_candidate"
	m.Evidence = &evidence
	return m, evidence.Duplicate, nil
}

func (p *plan) remapAudio(old, key string) {
	d := p.destination
	e := d.Entities[old]
	delete(d.Entities, old)
	e.Key = key
	d.Entities[key] = e
	if assignment := d.Entities["tag_assignment:"+old]; assignment != nil {
		delete(d.Entities, assignment.Key)
		assignment.Key = "tag_assignment:" + key
		assignment.Values["track"] = key
		d.Entities[assignment.Key] = assignment
	}
	d.State.Tracks[nativeTrackKey(e)] = key
	for _, other := range d.Entities {
		for i, member := range other.Members {
			if member == old {
				other.Members[i] = key
			}
		}
	}
}
