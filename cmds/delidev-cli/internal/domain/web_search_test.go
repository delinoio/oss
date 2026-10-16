// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func TestWebSearchObservationValidate(t *testing.T) {
	s := "q"
	valid := []WebSearchObservation{
		{Mode: WebSearchCached, Kind: WebSearchActionSearch, Search: &s, Queries: []string{"a"}},
		{Mode: WebSearchLive, Kind: WebSearchActionOpenPage, URL: &s},
		{Mode: WebSearchLive, Kind: WebSearchActionFindInPage, URL: &s, Pattern: &s},
		{Mode: WebSearchLive, Kind: WebSearchActionOther},
	}
	for _, o := range valid {
		if err := o.Validate(); err != nil {
			t.Fatal(o.Kind, err)
		}
	}
	invalid := []WebSearchObservation{
		{Mode: WebSearchDisabled, Kind: WebSearchActionOther},
		{Mode: "", Kind: WebSearchActionOther},
		{Mode: WebSearchLive, Kind: "browse"},
		{Mode: WebSearchLive, Kind: WebSearchActionSearch, URL: &s},
		{Mode: WebSearchLive, Kind: WebSearchActionOpenPage, Pattern: &s},
		{Mode: WebSearchLive, Kind: WebSearchActionOther, URL: &s},
		{Mode: WebSearchLive, Kind: WebSearchActionOther, Query: "bad\x00"},
	}
	for _, o := range invalid {
		if o.Validate() == nil {
			t.Fatal("accepted", o)
		}
	}
}

func TestArtifactSnapshotWebSearch(t *testing.T) {
	ok := ArtifactSnapshot{Kind: WebSearchArtifact, WebSearch: &WebSearchObservation{Mode: WebSearchLive, Kind: WebSearchActionOther}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []ArtifactSnapshot{
		{Kind: WebSearchArtifact},
		{Kind: WebSearchArtifact, WebSearch: ok.WebSearch, Text: "invented"},
		{Kind: PlanArtifact, WebSearch: ok.WebSearch},
		{Kind: ImageGenerationArtifact, WebSearch: ok.WebSearch, ImageGeneration: &ImageGenerationObservation{Status: ImageGenerationRunning, Outputs: []ImageAttachment{}}},
	} {
		if bad.Validate() == nil {
			t.Fatal("accepted", bad.Kind)
		}
	}
}
