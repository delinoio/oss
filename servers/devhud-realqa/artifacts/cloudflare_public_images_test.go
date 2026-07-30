package artifacts

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPublicImageWAFRateLimitFixtureIsArtifactOnly(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("cloudflare-public-images.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		ArtifactOnly bool `json:"artifact_only"`
		Rules        []struct {
			Phase           string   `json:"phase"`
			Action          string   `json:"action"`
			Expression      string   `json:"expression"`
			Characteristics []string `json:"characteristics"`
			Requests        int      `json:"requests_per_period"`
			Seconds         int      `json:"period_seconds"`
		} `json:"rules"`
		Provisions bool `json:"provisions_resources"`
		Deploys    bool `json:"deploys_resources"`
	}
	if err = json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	if !fixture.ArtifactOnly || fixture.Provisions || fixture.Deploys ||
		len(fixture.Rules) != 2 {
		t.Fatalf("fixture boundary = %#v", fixture)
	}
	rate := fixture.Rules[1]
	if rate.Phase != "http_ratelimit" || rate.Action != "block" ||
		rate.Requests != 300 || rate.Seconds != 60 ||
		len(rate.Characteristics) != 1 ||
		rate.Characteristics[0] != "ip.src" ||
		!strings.Contains(rate.Expression, `http.request.method eq "GET"`) ||
		!strings.Contains(rate.Expression, `"/i/"`) ||
		strings.Contains(rate.Expression, `"/uploads/"`) {
		t.Fatalf("rate-limit fixture = %#v", rate)
	}
}
