package domain

import "testing"

func TestProviderEndpointRejectsAmbiguousModelBase(t *testing.T) {
	for _, endpoint := range []string{"https://api.example.test/v1?", "https://api.example.test/v1#", "https://api.example.test/a/../v1", "https://api.example.test/./v1", "http://127.1/v1", "http://2130706433/v1", "https://api.example.test/v1%2fmodels", "https://user:secret@api.example.test/v1"} {
		if ValidateEndpoint(endpoint, false) == nil {
			t.Errorf("accepted ambiguous endpoint: %s", endpoint)
		}
	}
	for _, endpoint := range []string{"https://api.example.test/v1", "https://api.example.test/v1/", "http://127.0.0.1:1234/v1", "http://[::1]:1234/v1"} {
		if err := ValidateEndpoint(endpoint, false); err != nil {
			t.Errorf("rejected endpoint: %s %v", endpoint, err)
		}
	}
}
