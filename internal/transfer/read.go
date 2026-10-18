package transfer

import (
	"context"
	"fmt"
	"sort"
	"strconv"
)

func readCollection(ctx context.Context, q queryer, path string, current *state) (*collection, error) {
	c := &collection{Path: path, Entities: map[string]*entity{}, Playlists: map[int64]string{}, Schema: map[string][]string{}}
	var major, minor, patch int
	if err := q.QueryRowContext(ctx, "SELECT uuid,schemaVersionMajor,schemaVersionMinor,schemaVersionPatch FROM Information").Scan(&c.UUID, &major, &minor, &patch); err != nil {
		return nil, err
	}
	if c.UUID == "" || major != 3 || minor != 0 || patch != 2 {
		return nil, fmt.Errorf("unsupported Engine database schema %d.%d.%d; sync requires verified schema 3.0.2", major, minor, patch)
	}
	var err error
	c.State, err = loadState(path, c.UUID)
	if err != nil {
		return nil, err
	}
	if current != nil {
		c.State = *current
	}
	tables := map[string][]row{}
	for _, table := range []string{"Track", "PerformanceData", "AlbumArt", "Playlist", "PlaylistEntity", "Smartlist", "PreparelistEntity"} {
		tables[table], c.Schema[table], err = rows(ctx, q, table)
		if err != nil {
			return nil, err
		}
	}
	arts := map[int64]row{}
	for _, v := range tables["AlbumArt"] {
		arts[number(v["id"])] = without(v, "id")
	}
	perf := map[int64]row{}
	for _, v := range tables["PerformanceData"] {
		perf[number(v["trackId"])] = without(v, "trackId")
	}
	for _, v := range tables["Track"] {
		id := number(v["id"])
		origin := str(v["originDatabaseUuid"])
		oid := number(v["originTrackId"])
		if origin == "" || oid <= 0 {
			return nil, fmt.Errorf("track %d has no stable origin identity", id)
		}
		key := "track:" + identity(origin, oid)
		if c.Entities[key] != nil {
			return nil, fmt.Errorf("duplicate track origin identity %s", key)
		}
		c.Entities[key] = &entity{Kind: "track", Key: key, ID: id, Values: without(v, "id", "path", "filename", "albumArtId", "albumArt", "lastEditTime", "dateAdded", "isMetadataOfPackedTrackChanged", "isPerfomanceDataOfPackedTrackChanged", "playedIndicator", "isAvailable"), Performance: perf[id], Art: arts[number(v["albumArtId"])], File: mediaPath(path, str(v["path"]))}
	}
	for _, v := range tables["Playlist"] {
		id := number(v["id"])
		key := c.State.Playlists[strconv.FormatInt(id, 10)]
		if key == "" {
			key = "playlist:" + identity(c.UUID, id)
		}
		if c.Entities[key] != nil {
			return nil, fmt.Errorf("duplicate playlist identity %s", key)
		}
		c.Playlists[id] = key
		c.Entities[key] = &entity{Kind: "playlist", Key: key, ID: id, Values: without(v, "id", "parentListId", "nextListId", "lastEditTime", "isExplicitlyExported"), Members: []string{}}
	}
	for _, v := range tables["Playlist"] {
		e := c.Entities[c.Playlists[number(v["id"])]]
		e.Parent = c.Playlists[number(v["parentListId"])]
		e.Next = c.Playlists[number(v["nextListId"])]
		if number(v["parentListId"]) != 0 && e.Parent == "" {
			return nil, fmt.Errorf("playlist %d has missing parent", e.ID)
		}
		members := map[int64]row{}
		pointed := map[int64]bool{}
		for _, m := range tables["PlaylistEntity"] {
			if number(m["listId"]) == e.ID {
				members[number(m["id"])] = m
				pointed[number(m["nextEntityId"])] = true
			}
		}
		var head int64
		for id := range members {
			if !pointed[id] {
				if head != 0 {
					return nil, fmt.Errorf("playlist %d has multiple membership heads", e.ID)
				}
				head = id
			}
		}
		seen := map[int64]bool{}
		for head != 0 {
			m, ok := members[head]
			if !ok || seen[head] {
				return nil, fmt.Errorf("playlist %d has invalid membership order", e.ID)
			}
			seen[head] = true
			key := "track:" + identity(str(m["databaseUuid"]), number(m["trackId"]))
			e.Members = append(e.Members, key)
			head = number(m["nextEntityId"])
		}
		if len(seen) != len(members) {
			return nil, fmt.Errorf("playlist %d has cyclic membership order", e.ID)
		}
	}
	for _, v := range tables["Smartlist"] {
		key := "smartlist:" + str(v["listUuid"])
		c.Entities[key] = &entity{Kind: "smartlist", Key: key, Values: without(v, "lastEditTime")}
	}
	if prepared := tables["PreparelistEntity"]; len(prepared) > 0 {
		sort.Slice(prepared, func(i, j int) bool { return number(prepared[i]["trackNumber"]) < number(prepared[j]["trackNumber"]) })
		byID := map[int64]string{}
		for key, e := range c.Entities {
			if e.Kind == "track" {
				byID[e.ID] = key
			}
		}
		e := &entity{Kind: "preparelist", Key: "preparelist", Values: row{}, Members: []string{}}
		for _, v := range prepared {
			key := byID[number(v["trackId"])]
			if key == "" {
				return nil, fmt.Errorf("prepare list references a missing track")
			}
			e.Members = append(e.Members, key)
		}
		c.Entities[e.Key] = e
	}
	for _, e := range c.Entities {
		if e.Kind != "playlist" {
			continue
		}
		seen := map[string]bool{e.Key: true}
		for parent := e.Parent; parent != ""; parent = c.Entities[parent].Parent {
			if seen[parent] {
				return nil, fmt.Errorf("playlist %d has a cyclic parent hierarchy", e.ID)
			}
			seen[parent] = true
		}
	}
	return c, nil
}
