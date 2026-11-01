package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestGoldenRefusesDamagedTrackEvidence(t *testing.T) {
	for _, damage := range []string{"missing-track", "duplicate-number", "zero-number", "excess-number", "missing-title", "invalid-base64", "binary-title", "empty-title", "invalid-utf8", "nul-title"} {
		t.Run(damage, func(t *testing.T) {
			g := capturedGolden(t)
			v := g.Tracks[0].Values["title"]
			badTitle := true
			switch damage {
			case "missing-track":
				g.Tracks = g.Tracks[1:]
				badTitle = false
			case "duplicate-number":
				g.Tracks[0].Number = g.Tracks[1].Number
				badTitle = false
			case "zero-number":
				g.Tracks[0].Number = 0
				badTitle = false
			case "excess-number":
				g.Tracks[0].Number = len(g.Tracks) + 1
				badTitle = false
			case "missing-title":
				delete(g.Tracks[0].Values, "title")
			case "invalid-base64":
				v.Data = "!"
			case "binary-title":
				v.Type = "blob"
			case "empty-title":
				v.Data = ""
			case "invalid-utf8":
				v.Data = base64.StdEncoding.EncodeToString([]byte{0xff})
			case "nul-title":
				v.Data = base64.StdEncoding.EncodeToString([]byte("House\x00title"))
			}
			if badTitle && damage != "missing-title" {
				g.Tracks[0].Values["title"] = v
			}
			if err := validateGolden(g); err == nil {
				t.Fatal("damaged captured track evidence accepted")
			}
			if badTitle {
				data, err := wave(g.Tracks[0])
				if err == nil || len(data) != 0 {
					t.Fatalf("invalid captured title produced deliverable audio: bytes=%d err=%v", len(data), err)
				}
			}
			if err := validateGolden(capturedGolden(t)); err != nil {
				t.Fatalf("intact captured evidence rejected after refusal: %v", err)
			}
		})
	}
}

func TestGoldenRequiresAuthenticUniqueOpaqueRules(t *testing.T) {
	for _, name := range []string{"T1", "T3"} {
		for _, damage := range []string{"missing", "duplicate", "changed-rule", "changed-hash", "self-consistent-forgery"} {
			t.Run(name+"/"+damage, func(t *testing.T) {
				g := capturedGolden(t)
				index := -1
				for i, c := range g.Cases {
					if c.Name == name {
						index = i
						break
					}
				}
				if index < 0 {
					t.Fatal("captured rule case unavailable")
				}
				switch damage {
				case "missing":
					g.Cases = append(g.Cases[:index], g.Cases[index+1:]...)
				case "duplicate":
					g.Cases = append(g.Cases, g.Cases[index])
				case "changed-rule":
					g.Cases[index].Rules += " "
				case "changed-hash":
					g.Cases[index].SHA256 = strings.Repeat("0", 64)
				case "self-consistent-forgery":
					g.Cases[index].Rules += " "
					g.Cases[index].SHA256 = digest([]byte(g.Cases[index].Rules))
				}
				if err := validateGolden(g); err == nil {
					t.Fatal("missing, duplicated or unauthentic opaque rule accepted")
				}
				if err := validateGolden(capturedGolden(t)); err != nil {
					t.Fatalf("intact captured rules rejected after refusal: %v", err)
				}
			})
		}
	}
}
