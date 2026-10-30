package api

import "testing"

func TestMetadataTransformFailureHasNoResult(t *testing.T) {
	s, _ := testAPI(t)
	for _, test := range []struct{ operation, parameters string }{
		{"track.batch.capitalize", `{"identifiers":["999999"],"fields":["title"],"recipe":"upper","dry_run":true}`},
		{"track.batch.find-replace", `{"identifiers":["999999"],"fields":["title"],"find":"x","replacement":"y","dry_run":true}`},
	} {
		t.Run(test.operation, func(t *testing.T) {
			result, id, err := s.RunOperation(t.Context(), test.operation, []byte(test.parameters))
			if err == nil || result != nil || id != "" {
				t.Fatalf("failed review returned result=%#v history=%q error=%v", result, id, err)
			}
		})
	}
}
