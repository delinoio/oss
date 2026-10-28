// SPDX-License-Identifier: Apache-2.0
package inference

func forceMessagesTranslation(in Wire, target Target) bool {
	return target.ForceMessagesTranslation && in == Anthropic && target.Wire == Anthropic
}

func translateTargetRequest(body object, in Wire, target Target) (object, error) {
	if forceMessagesTranslation(in, target) {
		// Use the same canonical translation lane as cross-protocol clients. The
		// intermediate wire is private: the selected endpoint and auth stay Messages.
		canonical, err := translateRequest(body, Anthropic, Chat)
		if err != nil {
			return nil, err
		}
		return translateRequest(canonical, Chat, Anthropic)
	}
	return translateRequest(body, in, target.Wire)
}
