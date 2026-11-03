package leshy

import (
	"encoding/json"
	"testing"
)

func TestPublicationWithoutFamiliesDecodesZero(t *testing.T) {
	// Pre-K1 publications have no families field and no generation.
	old := `{"published":true,"zone":"primary","interface":"awg0"}`
	var publication PublicationState
	if err := json.Unmarshal([]byte(old), &publication); err != nil {
		t.Fatal(err)
	}
	if !publication.Published || publication.Zone != "primary" {
		t.Fatalf("legacy fields lost: %#v", publication)
	}
	if publication.Generation != 0 || publication.Families != (publication.Families) {
		t.Fatalf("families must be zero: %#v", publication)
	}
	if publication.Families.IPv6.Ready {
		t.Fatal("IPv6 readiness must never be inferred")
	}
}

func TestPublicationWithGenerationRoundTrips(t *testing.T) {
	publication := PublicationState{
		Published:  true,
		Zone:       "primary",
		Interface:  "awg0",
		Generation: 12,
	}
	encoded, err := json.Marshal(publication)
	if err != nil {
		t.Fatal(err)
	}
	var decoded PublicationState
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Generation != 12 {
		t.Fatalf("generation lost: %#v", decoded)
	}
}
