// SPDX-License-Identifier: Apache-2.0
package providers

// Exa is an TaiRouter-owned credential lane for the hosted search backend. It
// does not add a model adapter or modify the pinned upstream provider inventory.
const exaPresetJSON = `{"id":"exa","label":"Exa","adapter":"exa-search","baseUrl":"https://api.exa.ai","authKind":"key","credentialOnly":true,"liveModels":false,"apiKeyValidation":"unknown","catalogSource":"tairouter","note":"Personal API credential for hosted Exa search; no directly routable inference model."}`

func validExaKey(key string) bool {
	if len(key) == 0 || len(key) > 4096 {
		return false
	}
	for _, b := range []byte(key) {
		if b < 33 || b > 126 {
			return false
		}
	}
	return true
}
