package history

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

type smartlistRepairPlan struct {
	review     SmartlistRepairReview
	evidence   *smartlistEvidence
	links      *smartlistLinkPlan
	amendments *smartlistAmendmentPlan
}

// PreviewSmartlistRepair never enrolls storage or performs interrupted recovery.
func PreviewSmartlistRepair(ctx context.Context, lib string) (*SmartlistRepairReview, error) {
	dir, err := storeDir(lib)
	if err != nil {
		return nil, err
	}
	lib = filepath.Dir(filepath.Dir(dir))
	return ReadSmartlistSafe(ctx, lib, func(ctx context.Context) (*SmartlistRepairReview, error) {
		plan, err := planSmartlistRepair(ctx, lib)
		if err != nil {
			return nil, err
		}
		return &plan.review, nil
	})
}

// This owner runs under a coherent read or the protected publisher's lock.
func planSmartlistRepair(ctx context.Context, lib string) (*smartlistRepairPlan, error) {
	evidence, err := readSmartlistEvidence(ctx, lib)
	if err != nil {
		return nil, err
	}
	orders, err := readSmartlistOrderEvidence(ctx, lib, evidence)
	if err != nil {
		return nil, err
	}
	links, err := planSmartlistLinks(ctx, evidence, orders)
	if err != nil {
		return nil, err
	}
	amendments, err := planSmartlistDeletionAmendments(ctx, lib, evidence, orders, links)
	if err != nil {
		return nil, err
	}
	review := SmartlistRepairReview{Rows: make([]SmartlistRepairRow, 0, len(links.patches)), AmendedDeletes: len(amendments.deletions)}
	owners := make(map[string]string)
	for _, amendment := range amendments.deletions {
		for _, patch := range amendment.after.patches {
			owners[patch.before.UUID] = amendment.entryID
		}
	}
	for _, patch := range links.patches {
		row := SmartlistRepairRow{UUID: patch.before.UUID, Name: patch.before.Name, Action: "remove", DeleteEntryID: owners[patch.before.UUID]}
		if patch.after != nil {
			row.Action = "reconnect"
			review.Reconnected++
		} else {
			review.Removed++
		}
		review.Rows = append(review.Rows, row)
	}
	sort.Slice(review.Rows, func(i, j int) bool { return review.Rows[i].UUID < review.Rows[j].UUID })
	review.PlanToken, err = smartlistPlanToken(ctx, lib, evidence.hash, review)
	if err != nil {
		return nil, err
	}
	return &smartlistRepairPlan{review: review, evidence: evidence, links: links, amendments: amendments}, nil
}

func smartlistPlanToken(ctx context.Context, lib, current string, review SmartlistRepairReview) (string, error) {
	dir, err := storeDir(lib)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	if err := encoder.Encode([]any{filepath.Dir(filepath.Dir(dir)), current, review}); err != nil {
		return "", err
	}
	// Bind the actual portable entry bytes, not a re-encoded projection that
	// would silently ignore future fields. Evidence reading validated every
	// relevant native image against the references in these entries.
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return hex.EncodeToString(hash.Sum(nil)), nil
	}
	if err != nil {
		return "", err
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, file.Name()))
		if err != nil {
			return "", err
		}
		if err := encoder.Encode([]any{file.Name(), data}); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
